package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
)

func fixture() (domain.Project, domain.ReviewFeedback) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	p := domain.Project{ID: "project", Name: "review fixture", Plan: &domain.VideoPlan{
		ID: "plan", ProjectID: "project", FlowID: "flow", FramingID: "framing", CreatedAt: now,
		Shots: []domain.Shot{{ID: "shot", PlanID: "plan", Category: domain.ShotCategory("TEXT"), Duration: 4 * time.Second}, {ID: "other", PlanID: "plan", Index: 1, Category: domain.ShotCategory("TEXT"), Duration: 2 * time.Second}},
	}, Timeline: &domain.Timeline{ID: "timeline", ProjectID: "project", Entries: []domain.TimelineEntry{
		{ShotID: "shot", Start: 5 * time.Second, End: 9 * time.Second}, {ShotID: "other", Start: 9 * time.Second, End: 11 * time.Second},
	}, Elements: []domain.VisualElement{{ID: "text", ShotID: "shot", Type: "Text", Start: time.Second, End: 3 * time.Second}}}}
	d := time.Second
	f := domain.ReviewFeedback{ID: "feedback", ProjectID: p.ID, ShotID: "shot", ElementID: "text", Timestamp: time.Second, Duration: &d, Type: domain.FeedbackTypeComment, Note: "Please make the text larger", CreatedAt: now}
	return p, f
}

func newStore(t *testing.T, cfg StoreConfig) *Store {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("file store requires macOS or Linux")
	}
	if cfg.Root == "" {
		cfg.Root = t.TempDir()
	}
	s, err := NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestValidationTargetsAndIntervals(t *testing.T) {
	tests := []struct {
		name   string
		change func(*domain.Project, *domain.ReviewFeedback)
		valid  bool
	}{
		{"valid", func(*domain.Project, *domain.ReviewFeedback) {}, true},
		{"element end duration inclusive", func(_ *domain.Project, f *domain.ReviewFeedback) { *f.Duration = 2 * time.Second }, true},
		{"element end point exclusive", func(_ *domain.Project, f *domain.ReviewFeedback) { f.Timestamp = 3 * time.Second; f.Duration = nil }, false},
		{"element before start", func(_ *domain.Project, f *domain.ReviewFeedback) { f.Timestamp = 0 }, false},
		{"duration past element", func(_ *domain.Project, f *domain.ReviewFeedback) { *f.Duration = 3 * time.Second }, false},
		{"project relative element", func(_ *domain.Project, f *domain.ReviewFeedback) { f.ShotID = ""; f.Timestamp = 6 * time.Second }, true},
		{"wrong project relative coordinate", func(_ *domain.Project, f *domain.ReviewFeedback) { f.ShotID = "" }, false},
		{"shot relative point", func(_ *domain.Project, f *domain.ReviewFeedback) { f.ElementID = ""; f.Timestamp = 0; f.Duration = nil }, true},
		{"shot end", func(_ *domain.Project, f *domain.ReviewFeedback) {
			f.ElementID = ""
			f.Timestamp = 4 * time.Second
			f.Duration = nil
		}, false},
		{"project gap", func(_ *domain.Project, f *domain.ReviewFeedback) {
			f.ShotID = ""
			f.ElementID = ""
			f.Timestamp = 2 * time.Second
		}, true},
		{"project end", func(_ *domain.Project, f *domain.ReviewFeedback) {
			f.ShotID = ""
			f.ElementID = ""
			f.Timestamp = 11 * time.Second
			f.Duration = nil
		}, false},
		{"wrong project", func(_ *domain.Project, f *domain.ReviewFeedback) { f.ProjectID = "wrong" }, false},
		{"unknown shot", func(_ *domain.Project, f *domain.ReviewFeedback) { f.ShotID = "unknown" }, false},
		{"element different shot", func(_ *domain.Project, f *domain.ReviewFeedback) { f.ShotID = "other" }, false},
		{"unknown element", func(_ *domain.Project, f *domain.ReviewFeedback) { f.ElementID = "unknown" }, false},
		{"negative timestamp", func(_ *domain.Project, f *domain.ReviewFeedback) { f.Timestamp = -1 }, false},
		{"zero duration", func(_ *domain.Project, f *domain.ReviewFeedback) { *f.Duration = 0 }, false},
		{"negative duration", func(_ *domain.Project, f *domain.ReviewFeedback) { *f.Duration = -1 }, false},
		{"overflow duration", func(_ *domain.Project, f *domain.ReviewFeedback) { *f.Duration = math.MaxInt64 }, false},
		{"unknown type", func(_ *domain.Project, f *domain.ReviewFeedback) { f.Type = "edit" }, false},
		{"blank note", func(_ *domain.Project, f *domain.ReviewFeedback) { f.Note = " \n " }, false},
		{"approval no note", func(_ *domain.Project, f *domain.ReviewFeedback) { f.Type = domain.FeedbackTypeApproval; f.Note = "" }, true},
		{"change request", func(_ *domain.Project, f *domain.ReviewFeedback) { f.Type = domain.FeedbackTypeChangeRequest }, true},
		{"zero created at", func(_ *domain.Project, f *domain.ReviewFeedback) { f.CreatedAt = time.Time{} }, false},
		{"invalid year", func(_ *domain.Project, f *domain.ReviewFeedback) {
			f.CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		}, false},
		{"empty ID", func(_ *domain.Project, f *domain.ReviewFeedback) { f.ID = "" }, false},
		{"unsafe ID", func(_ *domain.Project, f *domain.ReviewFeedback) { f.ID = "../escape" }, false},
		{"NUL note", func(_ *domain.Project, f *domain.ReviewFeedback) { f.Note = "note\x00" }, false},
		{"invalid UTF8 note", func(_ *domain.Project, f *domain.ReviewFeedback) { f.Note = string([]byte{0xff}) }, false},
		{"missing plan", func(p *domain.Project, _ *domain.ReviewFeedback) { p.Plan = nil }, false},
		{"missing timeline", func(p *domain.Project, _ *domain.ReviewFeedback) { p.Timeline = nil }, false},
		{"unplaced shot", func(p *domain.Project, f *domain.ReviewFeedback) {
			p.Timeline.Entries = p.Timeline.Entries[1:]
			p.Timeline.Elements = nil
			f.ElementID = ""
		}, false},
		{"bad project linkage", func(p *domain.Project, _ *domain.ReviewFeedback) { p.Timeline.ProjectID = "wrong" }, false},
		{"bad duration", func(p *domain.Project, _ *domain.ReviewFeedback) { p.Timeline.Entries[0].End++ }, false},
		{"duplicate element", func(p *domain.Project, _ *domain.ReviewFeedback) {
			p.Timeline.Elements = append(p.Timeline.Elements, p.Timeline.Elements[0])
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, f := fixture()
			tc.change(&p, &f)
			err := Validate(p, f)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
}

func TestPersistenceOrderingAndOwnership(t *testing.T) {
	s := newStore(t, StoreConfig{})
	p, f := fixture()
	ctx := context.Background()
	if got, err := s.List(ctx, p.ID); err != nil || len(got) != 0 {
		t.Fatalf("initial list: %v %v", got, err)
	}
	_, want := fixture()
	for _, id := range []domain.ID{"z", "a", "A"} {
		f.ID = id
		if err := s.Append(ctx, p, f); err != nil {
			t.Fatal(err)
		}
	}
	f.ID = "early"
	f.CreatedAt = f.CreatedAt.Add(-time.Second)
	if err := s.Append(ctx, p, f); err != nil {
		t.Fatal(err)
	}
	*f.Duration = 3 * time.Second
	f.Note = "mutated input"
	s = newStore(t, StoreConfig{Root: s.root})
	got, err := s.List(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []domain.ID
	for _, item := range got {
		ids = append(ids, item.ID)
	}
	if !reflect.DeepEqual(ids, []domain.ID{"early", "A", "a", "z"}) {
		t.Fatalf("order: %v", ids)
	}
	for i, item := range got {
		expected := clone(want)
		expected.ID = item.ID
		if item.ID == "early" {
			expected.CreatedAt = expected.CreatedAt.Add(-time.Second)
		}
		if !reflect.DeepEqual(item, expected) {
			t.Fatalf("input aliased: %+v", item)
		}
		*got[i].Duration = 10 * time.Second
		got[i].Note = "mutated result"
	}
	again, err := s.List(ctx, p.ID)
	if err != nil || again[0].Note != want.Note || *again[0].Duration != time.Second {
		t.Fatalf("output aliased: %v %v", again, err)
	}
	// Exact duplicate IDs cannot overwrite an append, even with changed data.
	f = want
	f.ID = "a"
	f.Note = "replacement"
	if err := s.Append(ctx, p, f); !errors.Is(err, ErrConflict) {
		t.Fatalf("overwrite: %v", err)
	}
	if empty, err := s.List(ctx, "another-project"); err != nil || len(empty) != 0 {
		t.Fatalf("project isolation: %v %v", empty, err)
	}
}

func TestConcurrentAppendAtomicity(t *testing.T) {
	s := newStore(t, StoreConfig{})
	p, f := fixture()
	var wins atomic.Int32
	var wg sync.WaitGroup
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		s2 := newStore(t, StoreConfig{Root: s.root})
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s2.Append(ctx, p, f); err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				t.Errorf("duplicate publication: %v", err)
			}
			own := clone(f)
			own.ID = domain.ID(fmt.Sprintf("unique-%02d", i))
			if err := s2.Append(ctx, p, own); err != nil {
				t.Error(err)
			}
			if _, err := s2.List(ctx, p.ID); err != nil {
				t.Errorf("partial record exposed: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("winners: %d", wins.Load())
	}
	got, err := s.List(ctx, p.ID)
	if err != nil || len(got) != 21 {
		t.Fatalf("lost append: %d %v", len(got), err)
	}
	names, err := os.ReadDir(s.root)
	if err != nil || len(names) != 21 {
		t.Fatalf("staging leak: %d %v", len(names), err)
	}
}

func TestInvalidAppendCancellationAndLimits(t *testing.T) {
	p, f := fixture()
	s := newStore(t, StoreConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Append(ctx, p, f); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.List(ctx, p.ID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := s.Append(nil, p, f); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.List(nil, p.ID); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := (*Store)(nil).Append(context.Background(), p, f); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.List(context.Background(), "../escape"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	f.Timestamp = 3 * time.Second
	if err := s.Append(context.Background(), p, f); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if names, _ := os.ReadDir(s.root); len(names) != 0 {
		t.Fatal("invalid append wrote files")
	}
	_, f = fixture()
	small := newStore(t, StoreConfig{MaxRecordBytes: 32})
	if err := small.Append(context.Background(), p, f); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	for _, id := range []domain.ID{"one", "two"} {
		f.ID = id
		if err := s.Append(context.Background(), p, f); err != nil {
			t.Fatal(err)
		}
	}
	limited := newStore(t, StoreConfig{Root: s.root, MaxListRecords: 1})
	if _, err := limited.List(context.Background(), p.ID); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	limited = newStore(t, StoreConfig{Root: s.root, MaxRecordBytes: 32})
	if _, err := limited.List(context.Background(), p.ID); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(s.root, fileName(f)))
	if err != nil {
		t.Fatal(err)
	}
	// Enough bytes for either single record, insufficient for both together.
	limited = newStore(t, StoreConfig{Root: s.root, MaxListBytes: info.Size() + 1})
	if _, err := limited.List(context.Background(), p.ID); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
}

func TestRecordIntegrityAndSchema(t *testing.T) {
	for _, kind := range []string{"checksum", "schema", "project", "feedback-id", "type", "unknown-field", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			s := newStore(t, StoreConfig{})
			p, f := fixture()
			if err := s.Append(context.Background(), p, f); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(s.root, fileName(f))
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			var r record
			if err := json.Unmarshal(data, &r); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "checksum":
				r.Feedback.Note = "tampered note"
			case "schema":
				r.SchemaVersion++
			case "project":
				r.Feedback.ProjectID = "different"
			case "feedback-id":
				r.Feedback.ID = "different"
			case "type":
				r.Feedback.Type = "unknown"
			}
			if kind != "checksum" {
				r.Digest = checksum(r)
			}
			data, err = json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "unknown-field" {
				data = append([]byte(`{"unknown":true,`), data[1:]...)
			}
			if kind == "trailing" {
				data = append(data, []byte(` {}`)...)
			}
			if err := os.WriteFile(name, data, 0600); err != nil {
				t.Fatal(err)
			}
			if got, err := s.List(context.Background(), p.ID); got != nil || !errors.Is(err, ErrCorrupt) {
				t.Fatalf("record accepted: %v %v", got, err)
			}
		})
	}
}

func TestCreatedAtCanonicalizationAndHistoricalListing(t *testing.T) {
	s := newStore(t, StoreConfig{})
	p, f := fixture()
	want := clone(f)
	f.CreatedAt = f.CreatedAt.In(time.FixedZone("caller-zone", 2*60*60))
	if err := s.Append(context.Background(), p, f); err != nil {
		t.Fatal(err)
	}
	// Historical feedback remains available even after the authoritative target
	// is removed. New feedback against that removed target is rejected.
	p.Timeline.Elements = nil
	f.ID = "new"
	if err := s.Append(context.Background(), p, f); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	got, err := s.List(context.Background(), p.ID)
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("historical record: %v %v", got, err)
	}
}

func TestCorruptAndUnsafeFilesFailClosed(t *testing.T) {
	for _, kind := range []string{"corrupt", "renamed", "symlink", "directory", "oversized", "staging"} {
		t.Run(kind, func(t *testing.T) {
			s := newStore(t, StoreConfig{})
			p, f := fixture()
			ctx := context.Background()
			if err := s.Append(ctx, p, f); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(s.root, fileName(f))
			var err error
			switch kind {
			case "corrupt":
				err = os.WriteFile(name, []byte("{}"), 0600)
			case "renamed":
				f.ID = "wrong"
				err = os.Rename(name, filepath.Join(s.root, fileName(f)))
			case "symlink":
				outside := filepath.Join(t.TempDir(), "outside")
				data, e := os.ReadFile(name)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(outside, data, 0600); e != nil {
					t.Fatal(e)
				}
				if e = os.Remove(name); e != nil {
					t.Fatal(e)
				}
				err = os.Symlink(outside, name)
			case "directory":
				if e := os.Remove(name); e != nil {
					t.Fatal(e)
				}
				err = os.Mkdir(name, 0700)
			case "oversized":
				err = os.WriteFile(name, []byte(strings.Repeat("x", int(s.limit)+1)), 0600)
			case "staging":
				err = os.WriteFile(filepath.Join(s.root, ".feedback-abandoned"), []byte("partial"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.List(ctx, p.ID)
			if kind == "staging" {
				if err != nil || len(got) != 1 {
					t.Fatalf("staging affected listing: %v %v", got, err)
				}
				return
			}
			if err == nil || got != nil {
				t.Fatalf("unsafe listing accepted: %v %v", got, err)
			}
			if strings.Contains(err.Error(), s.root) {
				t.Fatal("error leaked root")
			}
		})
	}
}

func TestRootReplacementAndSymlink(t *testing.T) {
	s := newStore(t, StoreConfig{})
	p, f := fixture()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(s.root, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(StoreConfig{Root: alias}); !errors.Is(err, ErrUnsafePath) {
		t.Fatal(err)
	}
	if err := os.Rename(s.root, s.root+"-moved"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(s.root + "-moved") })
	if err := os.Mkdir(s.root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(context.Background(), p, f); !errors.Is(err, ErrUnsafePath) {
		t.Fatal(err)
	}
	if _, err := s.List(context.Background(), p.ID); !errors.Is(err, ErrUnsafePath) {
		t.Fatal(err)
	}
}
