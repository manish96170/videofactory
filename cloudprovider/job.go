package cloudprovider

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/manish96170/videofactory/domain"
)

type operation int

const (
	submit operation = iota
	poll
	cancelJob
	download
)

// Snapshot is safe logical status, not resumable execution authority. Credentials,
// prompts, transport errors and download locations are never included. New plus
// an exhausted/failed submit can mean remote acceptance is unknown: do not submit
// a new request ID to evade deduplication. Remote status and download are separate.
type Snapshot struct {
	Ref              JobRef `json:"ref"`
	Status           Status `json:"status"`
	Downloaded       bool   `json:"downloaded"`
	SubmitAttempts   int    `json:"submitAttempts"`
	PollAttempts     int    `json:"pollAttempts"`
	CancelAttempts   int    `json:"cancelAttempts"`
	DownloadAttempts int    `json:"downloadAttempts"`
}

type jobState struct {
	snapshot Snapshot
	artifact Artifact
	attempts [4]int
	failures [4]int
	blocked  [4]bool
	next     [4]time.Time
}

// Job is an in-memory, bounded protocol handle, not a worker or background task.
// Calls on one job are serialized by fail-fast ErrBusy. No callback runs under a
// mutex; Snapshot may be called from callbacks and sees the last completed step.
// Do not copy after use. Create one job per unique request/idempotency key.
type Job struct {
	adapter *Adapter
	request SubmitRequest
	shot    domain.ID
	quality domain.Quality
	mu      sync.Mutex
	busy    bool
	state   jobState
}

// NewJob snapshots a closed prompt/seed parameter vocabulary into bounded JSON.
// Inputs cannot select endpoints, headers, credentials, paths or runtime options.
// Caller-owned maps must remain stable during this snapshot.
func (a *Adapter) NewJob(req domain.GenerationRequest) (*Job, error) {
	if !handle(string(req.ID)) || !handle(string(req.ShotID)) || req.ProviderID != a.ID() ||
		(req.Quality != domain.QualityDraft && req.Quality != domain.QualityFinal) || len(req.Params) > 2 {
		return nil, ErrRequest
	}
	params := make(map[string]any, len(req.Params))
	for key, value := range req.Params {
		s, ok := value.(string)
		if !ok || !utf8.ValidString(s) || (key != "prompt" && key != "seed") || len(s) > 65536 || (key == "seed" && !handle(s)) {
			return nil, ErrRequest
		}
		params[key] = s
	}
	req.Params = params
	raw, err := json.Marshal(req)
	if err != nil || len(raw) > 128*1024 {
		return nil, ErrRequest
	}
	j := &Job{adapter: a, request: SubmitRequest{Version: 1, RequestID: req.ID, Payload: raw}, shot: req.ShotID, quality: req.Quality}
	j.state.snapshot = Snapshot{Ref: JobRef{ProviderID: a.ID(), RequestID: req.ID}, Status: New}
	return j, nil
}

func (j *Job) Snapshot() Snapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state.snapshot
}

// Each method below makes at most one transport attempt. A transient error is
// returned immediately; invoke the same method later using the same job. A
// WaitError provides the earliest next scheduling time without consuming budget.
func (j *Job) Submit(ctx context.Context, now time.Time) (Snapshot, error) {
	s, _, err := j.run(ctx, now, submit, nil)
	return s, err
}
func (j *Job) Poll(ctx context.Context, now time.Time) (Snapshot, error) {
	s, _, err := j.run(ctx, now, poll, nil)
	return s, err
}
func (j *Job) Cancel(ctx context.Context, now time.Time) (Snapshot, error) {
	s, _, err := j.run(ctx, now, cancelJob, nil)
	return s, err
}
func (j *Job) Download(ctx context.Context, now time.Time, sink Sink) (DownloadResult, error) {
	_, result, err := j.run(ctx, now, download, sink)
	return result, err
}

func (j *Job) run(ctx context.Context, now time.Time, op operation, sink Sink) (snapshot Snapshot, result DownloadResult, err error) {
	if err = checkContext(ctx); err != nil {
		return j.Snapshot(), result, err
	}
	j.mu.Lock()
	if j.busy {
		snapshot = j.state.snapshot
		j.mu.Unlock()
		return snapshot, result, ErrBusy
	}
	j.busy = true
	s := j.state
	j.mu.Unlock()
	defer func() {
		s.snapshot.SubmitAttempts, s.snapshot.PollAttempts = s.attempts[submit], s.attempts[poll]
		s.snapshot.CancelAttempts, s.snapshot.DownloadAttempts = s.attempts[cancelJob], s.attempts[download]
		j.mu.Lock()
		j.state, j.busy = s, false
		j.mu.Unlock()
		snapshot = s.snapshot
	}()
	a, p := j.adapter, j.adapter.policy
	status := s.snapshot.Status
	if (op == submit && status != New) ||
		((op == poll || op == cancelJob) && status != Queued && status != Running) ||
		(op == download && (status != Succeeded || s.snapshot.Downloaded)) {
		return snapshot, result, ErrState
	}
	if op == cancelJob {
		if _, ok := a.transport.(Canceler); !ok {
			return snapshot, result, ErrUnsupported
		}
	}
	if op == download && nilLike(sink) {
		return snapshot, result, ErrRequest
	}
	if s.blocked[op] || s.failures[op] >= p.MaxAttempts || (op == poll && s.attempts[op] >= p.MaxPolls) || (op != poll && s.attempts[op] >= p.MaxAttempts) {
		return snapshot, result, ErrExhausted
	}
	if now.IsZero() {
		return snapshot, result, ErrRequest
	}
	if now.Before(s.next[op]) {
		return snapshot, result, &WaitError{NotBefore: s.next[op]}
	}
	if err = a.reserve(now); err != nil {
		return snapshot, result, err
	}
	s.attempts[op]++
	callCtx, stop := context.WithTimeout(ctx, p.AttemptTimeout)
	defer stop()
	credential, callErr := a.source.Acquire(callCtx)
	if e := callCtx.Err(); e != nil {
		callErr = e
	} else if callErr != nil || nilLike(credential.value) {
		callErr = ErrAuth
	}
	var reply Reply
	if callErr == nil {
		switch op {
		case submit:
			req := j.request
			req.Payload = append(json.RawMessage(nil), req.Payload...)
			reply, callErr = a.transport.Submit(callCtx, credential, req)
		case poll:
			reply, callErr = a.transport.Poll(callCtx, credential, s.snapshot.Ref)
		case cancelJob:
			reply, callErr = a.transport.(Canceler).Cancel(callCtx, credential, s.snapshot.Ref)
		case download:
			result, callErr = j.transfer(callCtx, credential, s.snapshot.Ref, s.artifact, sink)
		}
	}
	// Discard late/error-accompanying output. Local cancellation does not assert
	// a remote terminal state, and is distinct from a successful Cancel reply.
	if e := callCtx.Err(); e != nil {
		// Keep classification evidence until failure sanitizes it. A timeout
		// must not turn a permanent rejection into a retryable failure, or
		// discard a server Retry-After/failed cleanup indication.
		callErr = errors.Join(e, callErr)
	}
	if callErr != nil {
		result = DownloadResult{}
		return snapshot, result, j.failure(&s, op, now, callErr)
	}
	if op == download {
		s.snapshot.Downloaded = true
		return snapshot, result, nil
	}
	if !validReply(reply, s.snapshot.Ref, status, p) {
		s.blocked[op] = true
		return snapshot, result, ErrResult
	}
	s.snapshot.Ref, s.snapshot.Status = reply.Ref, reply.Status
	if reply.Artifact != nil {
		s.artifact = *reply.Artifact
	}
	s.failures[op] = 0
	s.next[poll] = now.Add(max(p.MinInterval, reply.PollAfter))
	if op == cancelJob {
		s.next[cancelJob] = s.next[poll]
	}
	return snapshot, result, nil
}

func validReply(r Reply, ref JobRef, previous Status, p Policy) bool {
	if r.Ref.ProviderID != ref.ProviderID || r.Ref.RequestID != ref.RequestID || !handle(r.Ref.JobID) ||
		(ref.JobID != "" && r.Ref.JobID != ref.JobID) || r.PollAfter < 0 || r.PollAfter > p.MaxDelay {
		return false
	}
	switch r.Status {
	case Queued:
		if previous == Running {
			return false
		}
	case Running:
	case Succeeded:
		if r.Artifact == nil || !validArtifact(*r.Artifact, p.MaxDownloadBytes) {
			return false
		}
	case Failed, Canceled:
	default:
		return false
	}
	if r.Status != Succeeded && r.Artifact != nil {
		return false
	}
	return true
}

func validArtifact(a Artifact, limit int64) bool {
	if !handle(a.ID) || a.Size <= 0 || a.Size > limit || len(a.SHA256) != 64 || strings.ToLower(a.SHA256) != a.SHA256 {
		return false
	}
	_, err := hex.DecodeString(a.SHA256)
	return err == nil
}

// Only closed sentinels cross the boundary, including through Unwrap. Permanent
// causes take precedence over wrapped/joined transient classifications.
func (j *Job) failure(s *jobState, op operation, now time.Time, err error) error {
	s.failures[op]++
	for _, permanent := range []error{ErrCleanup, ErrAuth, ErrRequest, ErrResult, ErrUnsupported, ErrPermanent, ErrIntegrity, ErrSink} {
		if errors.Is(err, permanent) {
			s.blocked[op] = true
			return permanent
		}
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	transient := error(nil)
	for _, candidate := range []error{ErrRateLimited, ErrTransient, context.DeadlineExceeded} {
		if errors.Is(err, candidate) {
			transient = candidate
			break
		}
	}
	if transient == nil {
		s.blocked[op] = true
		return ErrDependency
	}
	delay := j.adapter.policy.RetryDelay
	var transportErr *TransportError
	if errors.As(err, &transportErr) && transportErr != nil {
		if transportErr.RetryAfter < 0 || transportErr.RetryAfter > j.adapter.policy.MaxDelay {
			// An excessive positive server delay stops this operation but still
			// gates other jobs: truncating it would retry the provider too early.
			if transportErr.RetryAfter > 0 {
				j.adapter.deferUntil(now.Add(transportErr.RetryAfter))
			}
			s.blocked[op] = true
			return ErrExhausted
		}
		delay = max(delay, transportErr.RetryAfter)
	}
	s.next[op] = now.Add(delay)
	j.adapter.deferUntil(s.next[op])
	return transient
}
