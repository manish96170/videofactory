package model

import (
	"context"
	"sort"
	"sync"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/resource"
)

type useToken struct{ owned bool }
type heldInstance struct {
	spec        LoadSpec
	estimate    resource.Estimate
	reservation resource.Request
	lease       resource.Lease
	stopped     bool
}
type entry struct {
	key             Key
	caps            domain.ModelCapabilities
	loader          Loader
	state           State
	held            *heldInstance
	token           *useToken
	recording       int
	observations    resource.Measurement
	historyFailures uint64
	lastCode        Code
}

// Manager uses one state mutex. It marks operation intent before external
// budget/history/loader/clock calls, all of which run OUTSIDE that mutex.
// Construct with NewManager: the zero value is intentionally unusable.
type Manager struct {
	mu      sync.Mutex
	budget  Budget
	history History
	cfg     Config
	entries map[Key]*entry
}

func NewManager(budget Budget, history History, cfg Config) (*Manager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, failure(CodeInvalidConfig, err)
	}
	if nilLike(budget) || nilLike(history) {
		return nil, failure(CodeInvalidConfig, ErrInvalidConfig)
	}
	if err := budget.Config().Validate(); err != nil {
		return nil, failure(CodeInvalidConfig, ErrInvalidConfig, err)
	}
	return &Manager{budget: budget, history: history, cfg: cfg, entries: make(map[Key]*entry)}, nil
}

func (m *Manager) valid() bool { return m != nil && m.entries != nil }

// Register freezes trusted declarations without loading anything. Revision must
// match Loader.Identity and Requirements.Revision. There is no replacement.
func (m *Manager) Register(ctx context.Context, caps domain.ModelCapabilities, loader Loader) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if !m.valid() {
		return failure(CodeInvalidConfig, ErrInvalidConfig)
	}
	if nilLike(loader) {
		return failure(CodeInvalidModel, ErrInvalidModel)
	}
	caps = resource.CloneCapabilities(caps)
	if err := resource.ValidateCapabilities(caps); err != nil {
		return failure(CodeInvalidModel, ErrInvalidModel, err)
	}
	if caps.Requirements == nil {
		return failure(CodeInvalidModel, ErrInvalidModel)
	}
	key := Key{ProviderID: caps.ProviderID, ModelID: caps.ModelID, Revision: caps.Requirements.Revision}
	if err := key.Validate(); err != nil {
		return failure(CodeInvalidModel, ErrInvalidModel, err)
	}
	identity := loader.Identity()
	if err := contextError(ctx); err != nil {
		return err
	}
	if identity != key {
		return failure(CodeIdentityMismatch, ErrIdentityMismatch)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := contextError(ctx); err != nil {
		return err
	}
	if _, ok := m.entries[key]; ok {
		return failure(CodeDuplicate, ErrDuplicate)
	}
	m.entries[key] = &entry{key: key, caps: caps, loader: loader, state: StateUnloaded}
	return nil
}

func busy(e *entry) bool {
	return e.recording != 0 || (e.state != StateIdle && e.state != StateUnloaded && e.state != StateQuarantined)
}

// Acquire admits the exact key/profile/workload, never a caller ResourcePlan.
// A handle has exclusive use ownership, not a shared reference. Its Release
// leaves the full model reservation held. Idle reuse needs the SAME profile and
// exact workload and an estimate no larger than the held reservation; otherwise
// callers must explicitly Unload (no nested allocation/deadlock or eviction).
// Successful loads with advisory timing-history failure return a VALID handle:
// inspect handle.ObservationError() and Snapshot.HistoryFailures. They are not
// load failures and must not free a valid backend merely because history is full.
func (m *Manager) Acquire(ctx context.Context, key Key, profileID string, workload domain.Workload) (*UseHandle, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if !m.valid() {
		return nil, failure(CodeInvalidConfig, ErrInvalidConfig)
	}
	if err := key.Validate(); err != nil {
		return nil, failure(CodeInvalidRequest, ErrInvalidRequest, err)
	}
	if err := resource.ValidateWorkload(workload); err != nil {
		return nil, failure(CodeInvalidRequest, ErrInvalidRequest, err)
	}
	m.mu.Lock()
	if err := contextError(ctx); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	e := m.entries[key]
	if e == nil {
		m.mu.Unlock()
		return nil, failure(CodeNotFound, ErrNotFound)
	}
	if busy(e) {
		m.mu.Unlock()
		return nil, failure(CodeBusy, ErrBusy)
	}
	if e.state == StateQuarantined {
		m.mu.Unlock()
		return nil, failure(CodeQuarantined, ErrQuarantined)
	}
	oldState, held := e.state, e.held
	if held != nil && (held.spec.Profile.ID != profileID || held.spec.Workload != workload) {
		m.mu.Unlock()
		return nil, failure(CodeNeedsUnload, ErrNeedsUnload)
	}
	e.state = StateReserving
	caps := resource.CloneCapabilities(e.caps)
	m.mu.Unlock()

	// Each operation gets authoritative current budget facts and coherent history.
	cfg := m.budget.Config()
	history := m.history.Snapshot()
	est, err := resource.EstimateRequest(caps, profileID, workload, cfg, history, m.cfg.SafetyMarginPercent)
	if c := contextError(ctx); c != nil {
		m.restore(e, oldState)
		return nil, failure(CodeCanceled, c, err)
	}
	if err != nil {
		m.restore(e, oldState)
		return nil, failure(CodeReservation, ErrReservation, err)
	}
	profile, err := resource.Profile(caps, profileID)
	if err != nil {
		m.restore(e, oldState)
		return nil, failure(CodeInvalidModel, ErrInvalidModel, err)
	}
	if held != nil {
		if !withinReservation(est.Request, held.reservation) {
			m.restore(e, oldState)
			return nil, failure(CodeNeedsUnload, ErrNeedsUnload)
		}
		if e.loader.Identity() != key {
			m.quarantine(e, CodeIdentityMismatch)
			return nil, operationError(ctx, nil, CodeIdentityMismatch, ErrIdentityMismatch)
		}
		// Cancellation on reuse clears ONLY fresh intent, not the preexisting load.
		return m.handoff(ctx, e, est, nil, false)
	}

	lease, err := m.budget.Acquire(ctx, est.Request)
	if !nilLike(lease) {
		held = &heldInstance{spec: LoadSpec{key, profile, workload}, estimate: cloneEstimate(est), reservation: est.Request, lease: lease, stopped: true}
		m.mu.Lock()
		e.held = held
		m.mu.Unlock()
	}
	if c := contextError(ctx); c != nil || err != nil || nilLike(lease) {
		cause := operationError(ctx, nil, CodeReservation, ErrReservation, err)
		if held != nil {
			cause = m.refund(e, cause)
		} else {
			m.restore(e, StateUnloaded)
		}
		return nil, cause
	}
	// No work starts on an expired grant or mismatched source.
	if e.loader.Identity() != key {
		cause := operationError(ctx, nil, CodeIdentityMismatch, ErrIdentityMismatch)
		return nil, m.refund(e, cause)
	}
	if c := contextError(ctx); c != nil {
		return nil, m.refund(e, c)
	}
	m.mu.Lock()
	e.state = StateLoading
	m.mu.Unlock()
	loadErr, advisory := m.load(ctx, e)
	if loadErr != nil {
		return nil, m.cleanupFailedLoad(ctx, e, loadErr)
	}
	return m.handoff(ctx, e, est, advisory, true)
}

func withinReservation(want, held resource.Request) bool {
	return want.RAMBytes <= held.RAMBytes && want.CPU <= held.CPU && want.GPU <= held.GPU &&
		want.Backend == held.Backend && (!want.Heavy || held.Heavy) && (!want.Exclusive || held.Exclusive)
}

func (m *Manager) restore(e *entry, state State) { m.mu.Lock(); e.state = state; m.mu.Unlock() }
func (m *Manager) quarantine(e *entry, code Code) {
	m.mu.Lock()
	e.state = StateQuarantined
	e.lastCode = code
	m.mu.Unlock()
}

func (m *Manager) handoff(ctx context.Context, e *entry, est resource.Estimate, advisory error, fresh bool) (*UseHandle, error) {
	// A final source lookup is external and can itself observe/cause cancellation.
	id := e.loader.Identity()
	if id != e.key {
		cause := operationError(ctx, nil, CodeIdentityMismatch, ErrIdentityMismatch, advisory)
		if fresh {
			return nil, m.cleanupFailedLoad(ctx, e, cause)
		}
		m.quarantine(e, CodeIdentityMismatch)
		return nil, cause
	}
	m.mu.Lock()
	if err := contextError(ctx); err != nil {
		if !fresh {
			e.state = StateIdle
		}
		m.mu.Unlock()
		if fresh {
			return nil, m.cleanupFailedLoad(ctx, e, failure(CodeCanceled, err, advisory))
		}
		return nil, err
	}
	token := &useToken{owned: true}
	e.token = token
	e.state = StateInUse
	e.held.estimate = cloneEstimate(est)
	h := &UseHandle{manager: m, entry: e, token: token, spec: cloneSpec(e.held.spec), estimate: cloneEstimate(est), observationError: advisory}
	m.mu.Unlock()
	return h, nil
}

func (m *Manager) Snapshot() Snapshot {
	if !m.valid() {
		return Snapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{Models: make([]ModelSnapshot, 0, len(m.entries))}
	for _, e := range m.entries {
		v := ModelSnapshot{Key: e.key, Capabilities: resource.CloneCapabilities(e.caps), State: e.state,
			Observations: resource.CloneMeasurement(e.observations), HistoryFailures: e.historyFailures, LastCode: e.lastCode}
		if h := e.held; h != nil {
			spec, est, req := cloneSpec(h.spec), cloneEstimate(h.estimate), h.reservation
			v.Spec, v.Estimate, v.Reservation, v.StoppedVerified = &spec, &est, &req, h.stopped
		}
		s.Models = append(s.Models, v)
	}
	sort.Slice(s.Models, func(i, j int) bool {
		a, b := s.Models[i].Key, s.Models[j].Key
		if a.ProviderID != b.ProviderID {
			return a.ProviderID < b.ProviderID
		}
		if a.ModelID != b.ModelID {
			return a.ModelID < b.ModelID
		}
		return a.Revision < b.Revision
	})
	return s
}
