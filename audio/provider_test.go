package audio

import (
	"context"
	"errors"
	"testing"
)

type testWorker struct {
	result TranscriptionResult
	err    error
	mutate func(TranscriptionRequest)
	calls  int
}

func (w *testWorker) Transcribe(ctx context.Context, req TranscriptionRequest) (TranscriptionResult, error) {
	w.calls++
	if w.mutate != nil {
		w.mutate(req)
	}
	return w.result, w.err
}

func TestAudioBoundariesRejectInvalidAssetsBeforeCloningOrCallbacks(t *testing.T) {
	invalid := testAsset()
	invalid.Metadata = map[string]any{"value": []string{"not a JSON tree"}}
	if _, err := NewFixtureVoiceProvider("fixture", map[string]VoiceResult{
		"script": {Asset: invalid, Words: testWords(), Cues: testCues()},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("fixture accepted invalid metadata: %v", err)
	}

	worker := &testWorker{result: TranscriptionResult{Words: testWords()}}
	p, err := NewLocalASRProvider("local-asr", worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Transcribe(context.Background(), TranscriptionRequest{ID: "asr-1", Asset: invalid}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ASR accepted invalid metadata: %v", err)
	}
	if worker.calls != 0 {
		t.Fatalf("worker called for rejected request: %d", worker.calls)
	}
}

func TestLocalASRBoundaryCopiesAndRejectsDependencyResults(t *testing.T) {
	worker := &testWorker{result: TranscriptionResult{Words: testWords(), Cues: testCues()}, mutate: func(req TranscriptionRequest) {
		req.Asset.Metadata["nested"].(map[string]any)["name"] = "worker mutation"
	}}
	p, err := NewLocalASRProvider("local-asr", worker)
	if err != nil {
		t.Fatal(err)
	}
	req := TranscriptionRequest{ID: "asr-1", Asset: testAsset()}
	got, err := p.Transcribe(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if req.Asset.Metadata["nested"].(map[string]any)["name"] != "narration" {
		t.Fatal("worker retained caller metadata")
	}
	got.Words[0].Word = "changed"
	again, err := p.Transcribe(context.Background(), req)
	if err != nil || again.Words[0].Word != "hello" {
		t.Fatalf("worker result alias: %+v, %v", again, err)
	}
	worker.err = errors.New("worker failed")
	worker.result.Words[0].Word = "payload must be ignored"
	if _, err := p.Transcribe(context.Background(), req); !errors.Is(err, ErrDependency) {
		t.Fatalf("error payload leaked: %v", err)
	}
}

func TestLocalASRHonorsCancellationBeforeWorker(t *testing.T) {
	worker := &testWorker{result: TranscriptionResult{Words: testWords()}}
	p, err := NewLocalASRProvider("local-asr", worker)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Transcribe(ctx, TranscriptionRequest{ID: "asr-1", Asset: testAsset()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestLocalASRInFlightCancellationRejectsLateSuccessfulResult(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	worker := &testWorker{result: TranscriptionResult{Words: testWords()}, mutate: func(TranscriptionRequest) {
		close(started)
		<-release
	}}
	p, err := NewLocalASRProvider("local-asr", worker)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultCh := make(chan TranscriptionResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := p.Transcribe(ctx, TranscriptionRequest{ID: "asr-1", Asset: testAsset()})
		resultCh <- result
		errCh <- err
	}()
	<-started
	cancel()
	close(release)
	result, gotErr := <-resultCh, <-errCh
	if !errors.Is(gotErr, context.Canceled) || len(result.Words) != 0 || len(result.Cues) != 0 {
		t.Fatalf("late successful result escaped cancellation: %+v, %v", result, gotErr)
	}
}

func TestAppleVoiceProviderIsExplicitlyUnsupported(t *testing.T) {
	p, err := NewAppleVoiceProvider("apple-tts")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Synthesize(context.Background(), VoiceRequest{ID: "r", ShotID: "s", Script: "hello", Voice: "voice"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
}

func TestLocalASRTypedNilWorkerRejected(t *testing.T) {
	var worker *testWorker
	if _, err := NewLocalASRProvider("local", worker); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v", err)
	}
}

var _ LocalASRWorker = (*testWorker)(nil)
