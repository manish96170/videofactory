package resource

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"math"
	"sort"
	"strconv"
	"sync"
)

// Allocation/QueueEntry are copied advisory records; no contexts or handles.
type Allocation struct {
	ID       string   `json:"id"`
	Request  Request  `json:"request"`
	Reserved Counters `json:"reserved"`
}

type QueueEntry struct {
	Request  Request  `json:"request"`
	Reserved Counters `json:"reserved"`
	Decision string   `json:"decision"` // fifo_wait; only the head can be granted
}

// Snapshot is a point-in-time copy, NOT a reservation. Config/Total are immutable
// manager facts; Used/Available/Active/Queue describe current logical ownership.
type Snapshot struct {
	Config    Config       `json:"config"`
	Total     Counters     `json:"total"`
	Used      Counters     `json:"used"`
	Available Counters     `json:"available"`
	Exclusive bool         `json:"exclusive"`
	Active    []Allocation `json:"active"`
	Queue     []QueueEntry `json:"queue"`
}

// AvailableNow is advisory, including FIFO/exclusive constraints. Permanent
// invalid/incompatible/over-budget failures are distinguished from a wait.
func (s Snapshot) AvailableNow(r Request) (bool, error) {
	if err := s.Config.Validate(); err != nil {
		return false, err
	}
	_, q, err := normalized(s.Config, r)
	if err != nil {
		return false, err
	}
	return len(s.Queue) == 0 && !s.Exclusive && (!r.Exclusive || s.Used.Slots == 0) && fits(q, s.Available), nil
}

type ticket struct {
	ctx   context.Context
	req   Request
	q     Counters
	ready chan struct{}
	lease *budgetLease
	err   error
}

// BudgetManager has one mutex, bounded FIFO tickets, owned allocation tokens,
// and integer counters. No polling goroutine/adapters/physical enforcement.
// Construct with NewManager; zero value is not usable. There is deliberately no
// Close: the caller/controller owns admission contexts and MUST stop live work
// and Release leases explicitly. Cancellation never expires handed-off leases.
type BudgetManager struct {
	mu        sync.Mutex
	cfg       Config
	total     Counters
	used      Counters
	exclusive bool
	prefix    string
	next      uint64
	active    map[uint64]Allocation
	queue     []*ticket
}

var _ Manager = (*BudgetManager)(nil)

func NewManager(cfg Config) (*BudgetManager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, ErrInvalidConfig
	}
	return &BudgetManager{cfg: cfg, total: totals(cfg), prefix: hex.EncodeToString(nonce[:]), active: make(map[uint64]Allocation)}, nil
}

func (m *BudgetManager) Config() Config { return m.cfg }

func (m *BudgetManager) Acquire(ctx context.Context, req Request) (Lease, error) {
	if ctx == nil {
		return nil, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m == nil || m.active == nil {
		return nil, ErrInvalidConfig
	}
	req, q, err := normalized(m.cfg, req)
	if err != nil {
		return nil, err
	}
	t := &ticket{ctx: ctx, req: req, q: q, ready: make(chan struct{})}
	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	// Existing canceled tickets cannot consume queue space indefinitely.
	m.schedule()
	if len(m.queue) == 0 && m.canGrant(t) {
		m.grant(t)
	} else if len(m.queue) >= m.cfg.QueueLimit {
		m.mu.Unlock()
		return nil, ErrQueueFull
	} else {
		m.queue = append(m.queue, t)
	}
	m.mu.Unlock()
	select {
	case <-t.ready:
	case <-ctx.Done():
	}
	m.mu.Lock()
	// Check under ownership lock before the final handoff. A racing observed
	// cancellation rolls back even if grant notification won the select.
	if err := ctx.Err(); err != nil {
		if t.lease != nil {
			m.release(t.lease.token)
		} else {
			m.remove(t)
		}
		m.schedule()
		m.mu.Unlock()
		return nil, err
	}
	if t.err != nil {
		m.mu.Unlock()
		return nil, t.err
	}
	lease := t.lease
	m.mu.Unlock()
	return lease, nil
}

func (m *BudgetManager) canGrant(t *ticket) bool {
	return !m.exclusive && (!t.req.Exclusive || m.used.Slots == 0) && fits(t.q, subtract(m.total, m.used))
}

func (m *BudgetManager) grant(t *ticket) {
	if m.next == math.MaxUint64 {
		t.err = ErrOverflow
		close(t.ready)
		return
	}
	m.next++
	id := m.prefix + "-" + strconv.FormatUint(m.next, 10)
	m.active[m.next] = Allocation{ID: id, Request: t.req, Reserved: t.q}
	m.used = add(m.used, t.q)
	m.exclusive = t.req.Exclusive
	t.lease = &budgetLease{manager: m, token: m.next, id: id}
	close(t.ready)
}

func (m *BudgetManager) remove(t *ticket) {
	for i, v := range m.queue {
		if v == t {
			copy(m.queue[i:], m.queue[i+1:])
			m.queue[len(m.queue)-1] = nil
			m.queue = m.queue[:len(m.queue)-1]
			return
		}
	}
}

// schedule removes canceled tickets anywhere, then grants FIFO heads only.
// Head-of-line blocking is intentional: small later requests never bypass it.
func (m *BudgetManager) schedule() {
	for i := 0; i < len(m.queue); {
		t := m.queue[i]
		if err := t.ctx.Err(); err != nil {
			m.remove(t)
			t.err = err
			close(t.ready)
		} else {
			i++
		}
	}
	for len(m.queue) > 0 {
		t := m.queue[0]
		if !m.canGrant(t) {
			break
		}
		m.remove(t)
		m.grant(t)
	}
}

func (m *BudgetManager) release(token uint64) {
	if a, ok := m.active[token]; ok {
		delete(m.active, token)
		m.used = subtract(m.used, a.Reserved)
		if a.Request.Exclusive {
			m.exclusive = false
		}
	}
}

func (m *BudgetManager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{Config: m.cfg, Total: m.total, Used: m.used, Available: subtract(m.total, m.used), Exclusive: m.exclusive,
		Active: make([]Allocation, 0, len(m.active)), Queue: make([]QueueEntry, 0, len(m.queue))}
	tokens := make([]uint64, 0, len(m.active))
	for token := range m.active {
		tokens = append(tokens, token)
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i] < tokens[j] })
	for _, token := range tokens {
		s.Active = append(s.Active, m.active[token])
	}
	for _, t := range m.queue {
		s.Queue = append(s.Queue, QueueEntry{Request: t.req, Reserved: t.q, Decision: "fifo_wait"})
	}
	return s
}

type budgetLease struct {
	manager *BudgetManager
	token   uint64
	id      string
}

func (l *budgetLease) ID() string { return l.id }
func (l *budgetLease) Release() error {
	m := l.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	m.release(l.token)
	m.schedule()
	return nil
}
