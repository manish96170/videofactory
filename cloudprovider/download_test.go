package cloudprovider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type readFunc func([]byte) (int, error)

func (f readFunc) Read(b []byte) (int, error) { return f(b) }

func downloadable(t *testing.T, artifact Artifact, downloadFn func(context.Context, Credential, JobRef, Artifact) (io.ReadCloser, error), c Config) *Job {
	t.Helper()
	tr := &transportStub{
		submit: func(_ context.Context, _ Credential, r SubmitRequest) (Reply, error) {
			v := queued(r)
			v.Status = Succeeded
			v.Artifact = &artifact
			return v, nil
		},
		download: downloadFn,
	}
	j := newJob(t, newAdapter(t, c, tr, nil))
	if _, err := j.Submit(context.Background(), epoch); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestDownloadIntegrityAndStagingFailures(t *testing.T) {
	data := []byte("fixture bytes")
	for _, name := range []string{"short", "long", "digest", "read-error", "no-progress", "short-write", "write-error", "close-error", "begin-error", "commit-error", "abort-error"} {
		t.Run(name, func(t *testing.T) {
			a := fixtureArtifact(data)
			stream := &readerStub{Reader: bytes.NewReader(data)}
			stage := &stageStub{}
			sink := &sinkStub{stage: stage}
			want := ErrIntegrity
			switch name {
			case "short":
				stream.Reader = bytes.NewReader(data[:len(data)-1])
			case "long":
				stream.Reader = bytes.NewReader(append(append([]byte(nil), data...), 1))
			case "digest":
				a.SHA256 = strings.Repeat("0", 64)
			case "read-error":
				stream.Reader = readFunc(func([]byte) (int, error) { return 0, errors.New("raw secret") })
				want = ErrDependency
			case "no-progress":
				stream.Reader = readFunc(func([]byte) (int, error) { return 0, nil })
				want = ErrDependency
			case "short-write":
				stage.write = func(b []byte) (int, error) { return len(b) - 1, nil }
				want = ErrSink
			case "write-error":
				stage.write = func([]byte) (int, error) { return 0, errors.New("raw secret") }
				want = ErrSink
			case "close-error":
				stream.closeErr = errors.New("raw secret")
				want = ErrDependency
			case "begin-error":
				sink.beginErr = errors.New("raw secret")
				want = ErrSink
			case "commit-error":
				stage.commit = func(context.Context) error { return errors.New("raw secret") }
				want = ErrSink
			case "abort-error":
				stream.Reader = bytes.NewReader(nil)
				stage.abort = func(context.Context) error { return errors.New("raw secret") }
				want = ErrCleanup
			}
			j := downloadable(t, a, func(context.Context, Credential, JobRef, Artifact) (io.ReadCloser, error) { return stream, nil }, config())
			result, err := j.Download(context.Background(), epoch.Add(time.Second), sink)
			if !errors.Is(err, want) || result != (DownloadResult{}) || j.Snapshot().Downloaded {
				t.Fatalf("failure published: %+v %v", result, err)
			}
			if strings.Contains(err.Error(), "raw secret") {
				t.Fatal("error leaked")
			}
			if stream.closes != 1 || stage.aborts != 1 || stage.Len() != 0 {
				t.Fatalf("cleanup lost: closed=%d aborted=%d bytes=%d", stream.closes, stage.aborts, stage.Len())
			}
			if name != "commit-error" && stage.commits != 0 {
				t.Fatal("unverified bytes committed")
			}
			if _, err := j.Download(context.Background(), epoch.Add(time.Hour), sink); err != ErrExhausted {
				t.Fatal("permanent failure retried", err)
			}
		})
	}
}

func TestDownloadErrorPayloadAndTypedNil(t *testing.T) {
	a := fixtureArtifact([]byte("media"))
	stream := &readerStub{Reader: strings.NewReader("discard")}
	j := downloadable(t, a, func(context.Context, Credential, JobRef, Artifact) (io.ReadCloser, error) {
		return stream, &TransportError{Kind: ErrTransient}
	}, config())
	sink := &sinkStub{stage: &stageStub{}}
	if _, err := j.Download(context.Background(), epoch.Add(time.Second), sink); err != ErrTransient {
		t.Fatal(err)
	}
	if stream.closes != 1 || sink.calls != 0 {
		t.Fatal("used error-accompanying payload")
	}
	var missingSink *sinkStub
	if _, err := j.Download(context.Background(), epoch.Add(time.Hour), missingSink); err != ErrRequest {
		t.Fatal(err)
	}
	var missingStream *readerStub
	j = downloadable(t, a, func(context.Context, Credential, JobRef, Artifact) (io.ReadCloser, error) { return missingStream, nil }, config())
	if _, err := j.Download(context.Background(), epoch.Add(time.Second), sink); err != ErrResult {
		t.Fatal(err)
	}
	j = downloadable(t, a, func(context.Context, Credential, JobRef, Artifact) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("media")), nil
	}, config())
	if _, err := j.Download(context.Background(), epoch.Add(time.Second), &sinkStub{}); err != ErrSink {
		t.Fatal(err)
	}
}

func TestDownloadCancellationAtEveryBoundary(t *testing.T) {
	for _, boundary := range []string{"transport", "begin", "read", "write", "commit"} {
		t.Run(boundary, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			data := []byte("media")
			stream := &readerStub{Reader: bytes.NewReader(data)}
			stage := &stageStub{}
			sink := &sinkStub{stage: stage}
			switch boundary {
			case "begin":
				sink.begin = func(context.Context, Target) { cancel() }
			case "read":
				stream.Reader = readFunc(func(b []byte) (int, error) { cancel(); return copy(b, data), io.EOF })
			case "write":
				stage.write = func(b []byte) (int, error) { cancel(); return len(b), nil }
			case "commit":
				stage.commit = func(context.Context) error { cancel(); return nil }
			}
			stage.abort = func(cleanup context.Context) error {
				if cleanup.Err() != nil {
					t.Error("cleanup inherited cancellation")
				}
				if _, ok := cleanup.Deadline(); !ok {
					t.Error("cleanup unbounded")
				}
				return nil
			}
			j := downloadable(t, fixtureArtifact(data), func(context.Context, Credential, JobRef, Artifact) (io.ReadCloser, error) {
				if boundary == "transport" {
					cancel()
				}
				return stream, nil
			}, config())
			result, err := j.Download(ctx, epoch.Add(time.Second), sink)
			if err != context.Canceled || result != (DownloadResult{}) || j.Snapshot().Downloaded {
				t.Fatalf("late success %+v %v", result, err)
			}
			if stream.closes != 1 {
				t.Fatal("stream leaked")
			}
			if boundary != "transport" && stage.aborts != 1 {
				t.Fatal("staging not aborted")
			}
		})
	}
}

func TestDownloadTransientReadCanRetryFromFreshStaging(t *testing.T) {
	data := []byte("media")
	calls := 0
	j := downloadable(t, fixtureArtifact(data), func(context.Context, Credential, JobRef, Artifact) (io.ReadCloser, error) {
		calls++
		if calls == 1 {
			return io.NopCloser(readFunc(func(b []byte) (int, error) { return copy(b, "m"), ErrTransient })), nil
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}, config())
	first := &stageStub{}
	if _, err := j.Download(context.Background(), epoch.Add(time.Second), &sinkStub{stage: first}); err != ErrTransient {
		t.Fatal(err)
	}
	if first.aborts != 1 || first.commits != 0 || first.Len() != 0 {
		t.Fatal("retry retained partial bytes")
	}
	second := &stageStub{}
	if _, err := j.Download(context.Background(), epoch.Add(3*time.Second), &sinkStub{stage: second}); err != nil {
		t.Fatal(err)
	}
	if second.commits != 1 || !bytes.Equal(second.Bytes(), data) {
		t.Fatal("retry corrupted content")
	}
}

func TestCooperativeTimeoutAndCredentialCancellation(t *testing.T) {
	c := config()
	c.Policy.AttemptTimeout = time.Millisecond
	calls := 0
	tr := &transportStub{submit: func(ctx context.Context, _ Credential, r SubmitRequest) (Reply, error) {
		calls++
		<-ctx.Done()
		return queued(r), nil
	}}
	j := newJob(t, newAdapter(t, c, tr, nil))
	if s, err := j.Submit(context.Background(), epoch); err != context.DeadlineExceeded || s.Status != New {
		t.Fatalf("late timeout %+v %v", s, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := newAdapter(t, config(), tr, sourceFunc(func(context.Context) (Credential, error) { cancel(); return NewCredential("synthetic"), nil }))
	j = newJob(t, a)
	if _, err := j.Submit(ctx, epoch); err != context.Canceled || calls != 1 {
		t.Fatal("transport invoked after cancellation", err)
	}
}

func TestLatePermanentFailureNeverRetries(t *testing.T) {
	c := config()
	c.Policy.AttemptTimeout = time.Millisecond
	calls := 0
	tr := &transportStub{submit: func(ctx context.Context, _ Credential, r SubmitRequest) (Reply, error) {
		calls++
		<-ctx.Done()
		return queued(r), errors.Join(errors.New("synthetic secret"), ErrAuth)
	}}
	j := newJob(t, newAdapter(t, c, tr, nil))
	if s, err := j.Submit(context.Background(), epoch); err != ErrAuth || s.Status != New {
		t.Fatalf("permanent rejection hidden: %+v %v", s, err)
	}
	if _, err := j.Submit(context.Background(), epoch.Add(time.Hour)); err != ErrExhausted || calls != 1 {
		t.Fatal("late permanent rejection retried", err)
	}
}
