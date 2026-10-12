package changeset_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manish96170/videofactory/changeset"
	"github.com/manish96170/videofactory/domain"
)

type providerFunc func(context.Context, changeset.IntentRequest) (changeset.Intent, error)

func (f providerFunc) Propose(ctx context.Context, r changeset.IntentRequest) (changeset.Intent, error) {
	return f(ctx, r)
}

func chat() changeset.Request {
	return changeset.Request{ID: "edit", ProjectID: "project", CreatedAt: now, Text: "Change title"}
}

func TestEngineReviewDefaultAndAutomaticValidationGate(t *testing.T) {
	for _, mode := range []changeset.Mode{"", changeset.Review, changeset.Automatic} {
		t.Run(string(mode), func(t *testing.T) {
			var calls int
			e, err := changeset.New(providerFunc(func(ctx context.Context, r changeset.IntentRequest) (changeset.Intent, error) {
				calls++
				if r.BaseHash == "" || r.Request.Text != "Change title" {
					t.Fatal("lost context")
				}
				return changeset.Intent{Changes: []domain.Change{editText("New")}}, nil
			}), changeset.Config{Mode: mode})
			if err != nil {
				t.Fatal(err)
			}
			p := fixture()
			result, err := e.Execute(context.Background(), p, chat())
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || result.Validated == nil || (result.Applied != nil) != (mode == changeset.Automatic) {
				t.Fatalf("wrong mode result: %+v", result)
			}
			if p.Plan.Shots[0].Primitives[0].Properties["text"] != "Old" {
				t.Fatal("caller was applied in place")
			}
			s, _ := result.Validated.Snapshot()
			if s.ChangeSet.Source != "director-chat" {
				t.Fatal("source")
			}
		})
	}
	e, _ := changeset.New(providerFunc(func(context.Context, changeset.IntentRequest) (changeset.Intent, error) {
		return changeset.Intent{Changes: []domain.Change{{TargetID: "a", Op: "execute-provider"}}}, nil
	}), changeset.Config{Mode: changeset.Automatic})
	result, err := e.Execute(context.Background(), fixture(), chat())
	if !errors.Is(err, changeset.ErrUnsupported) || result.Validated != nil || result.Applied != nil {
		t.Fatalf("automatic validation bypass: %+v %v", result, err)
	}
}

func TestFeedbackAndChatContextCopiedThroughSingleIntentPath(t *testing.T) {
	for _, feedback := range []bool{false, true} {
		t.Run(map[bool]string{false: "chat", true: "feedback"}[feedback], func(t *testing.T) {
			p := fixture()
			req := chat()
			timestamp := time.Second
			duration := time.Second / 2
			if feedback {
				req.Text = ""
				req.Feedback = &domain.ReviewFeedback{ID: "feedback", ProjectID: p.ID, ShotID: "a", ElementID: "title", Timestamp: timestamp, Duration: &duration, Type: domain.FeedbackTypeChangeRequest, Note: "Change selected title", CreatedAt: now}
			} else {
				req.Target = changeset.Target{ShotID: "a", ElementID: "title", Timestamp: &timestamp, Duration: &duration}
			}
			intent := changeset.Intent{Changes: []domain.Change{editText("New")}}
			e, err := changeset.New(providerFunc(func(_ context.Context, in changeset.IntentRequest) (changeset.Intent, error) {
				if !reflect.DeepEqual(in.Request, req) {
					t.Fatalf("lost context: %+v", in.Request)
				}
				in.Project.Plan.Shots[0].Primitives[0].Properties["text"] = "provider mutation"
				in.Project.Timeline.Elements[0].Bounds.Width = .2
				in.Project.Assets[0].Metadata["nested"].(map[string]any)["values"].([]any)[0] = "bad"
				if feedback {
					*in.Request.Feedback.Duration = 0
					in.Request.Feedback.ProjectID = "bad"
				} else {
					*in.Request.Target.Duration = 0
					*in.Request.Target.Timestamp = 0
				}
				return intent, nil
			}), changeset.Config{})
			if err != nil {
				t.Fatal(err)
			}
			v, err := e.Propose(context.Background(), p, req)
			if err != nil {
				t.Fatal(err)
			}
			intent.Changes[0].Payload["text"] = "returned alias"
			if duration != time.Second/2 || timestamp != time.Second || p.Plan.Shots[0].Primitives[0].Properties["text"] != "Old" || p.Timeline.Elements[0].Bounds.Width != 1 {
				t.Fatal("provider aliases caller")
			}
			r, err := changeset.Apply(context.Background(), p, v)
			if err != nil {
				t.Fatal(err)
			}
			if r.Project.Plan.Shots[0].Primitives[0].Properties["text"] != "New" || r.Project.Assets[0].Metadata["nested"].(map[string]any)["values"].([]any)[0] != "keep" {
				t.Fatal("provider mutated retained state")
			}
			if feedback && r.Preview.ChangeSet.Source != "review-feedback" {
				t.Fatal("feedback took separate mutation path")
			}
		})
	}
}

func TestInvalidTargetsNeverCallProvider(t *testing.T) {
	var calls atomic.Int32
	e, _ := changeset.New(providerFunc(func(context.Context, changeset.IntentRequest) (changeset.Intent, error) {
		calls.Add(1)
		return changeset.Intent{}, nil
	}), changeset.Config{})
	end := 2 * time.Second
	negative := -time.Second
	duration := time.Second
	targets := []changeset.Target{
		{ShotID: "missing"}, {ShotID: "b", ElementID: "title"}, {ElementID: "missing"},
		{ShotID: "a", Timestamp: &end}, {ShotID: "a", Timestamp: &negative}, {ShotID: "a", Duration: &duration},
	}
	for _, target := range targets {
		req := chat()
		req.Target = target
		if _, err := e.Propose(context.Background(), fixture(), req); !errors.Is(err, changeset.ErrInvalid) {
			t.Fatalf("target %+v: %v", target, err)
		}
	}
	req := chat()
	req.Feedback = &domain.ReviewFeedback{ID: "feedback", ProjectID: "project", Type: domain.FeedbackTypeComment, Note: "comment", CreatedAt: now}
	if _, err := e.Propose(context.Background(), fixture(), req); !errors.Is(err, changeset.ErrInvalid) {
		t.Fatal("ambiguous chat+feedback")
	}
	if calls.Load() != 0 {
		t.Fatal("provider called on invalid target")
	}
}

func TestApprovalFeedbackCannotProposeOrAutomaticallyApplyEdits(t *testing.T) {
	for _, note := range []string{"", "Approved; change the title"} {
		t.Run(note, func(t *testing.T) {
			var calls int
			e, err := changeset.New(providerFunc(func(context.Context, changeset.IntentRequest) (changeset.Intent, error) {
				calls++
				return changeset.Intent{Changes: []domain.Change{editText("Unexpected edit")}}, nil
			}), changeset.Config{Mode: changeset.Automatic})
			if err != nil {
				t.Fatal(err)
			}
			p := fixture()
			req := chat()
			req.Text = ""
			req.Feedback = &domain.ReviewFeedback{ID: "approval", ProjectID: p.ID, ShotID: "a", ElementID: "title",
				Timestamp: time.Second, Type: domain.FeedbackTypeApproval, Note: note, CreatedAt: now}
			v, err := e.Propose(context.Background(), p, req)
			if !errors.Is(err, changeset.ErrInvalid) || v != nil {
				t.Fatalf("approval produced edit proposal: %v %v", v, err)
			}
			out, err := e.Execute(context.Background(), p, req)
			if !errors.Is(err, changeset.ErrInvalid) || out.Validated != nil || out.Applied != nil {
				t.Fatalf("approval produced automatic edit: %+v %v", out, err)
			}
			if calls != 0 || !reflect.DeepEqual(p, fixture()) {
				t.Fatal("approval invoked provider or mutated caller project")
			}
		})
	}
}

func TestLateAndErrorProviderResultsIgnored(t *testing.T) {
	for _, kind := range []string{"error", "parent-cancel", "child-timeout"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls int
			e, _ := changeset.New(providerFunc(func(child context.Context, _ changeset.IntentRequest) (changeset.Intent, error) {
				calls++
				var err error
				switch kind {
				case "error":
					err = errors.New("SECRET upstream payload")
				case "parent-cancel":
					cancel()
				case "child-timeout":
					<-child.Done()
				}
				return changeset.Intent{Changes: []domain.Change{editText("New")}}, err
			}), changeset.Config{Mode: changeset.Automatic, Timeout: time.Millisecond})
			out, err := e.Execute(ctx, fixture(), chat())
			want := changeset.ErrProvider
			if kind == "parent-cancel" {
				want = context.Canceled
			}
			if kind == "child-timeout" {
				want = context.DeadlineExceeded
			}
			if !errors.Is(err, want) || out.Validated != nil || out.Applied != nil || calls != 1 {
				t.Fatalf("late/error payload accepted: %+v %v", out, err)
			}
		})
	}
}

func TestEngineBoundaryAndConcurrentCalls(t *testing.T) {
	var nilFunc providerFunc
	if _, err := changeset.New(nilFunc, changeset.Config{}); !errors.Is(err, changeset.ErrInvalid) {
		t.Fatal("typed nil accepted")
	}
	p := providerFunc(func(context.Context, changeset.IntentRequest) (changeset.Intent, error) {
		return changeset.Intent{Changes: []domain.Change{editText("New")}}, nil
	})
	for _, config := range []changeset.Config{{Mode: "future"}, {Timeout: -1}, {Timeout: 2 * time.Minute}} {
		if _, err := changeset.New(p, config); !errors.Is(err, changeset.ErrInvalid) {
			t.Fatal("invalid config accepted")
		}
	}
	e, _ := changeset.New(p, changeset.Config{Mode: changeset.Automatic})
	if _, err := e.Propose(nil, fixture(), chat()); !errors.Is(err, changeset.ErrInvalid) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := e.Execute(context.Background(), fixture(), chat())
			if err != nil || r.Applied == nil {
				t.Errorf("concurrent execute: %v", err)
			}
		}()
	}
	wg.Wait()
}
