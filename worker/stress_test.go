//go:build darwin || linux

package worker

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestRepeatedConcurrentRunnerOperationsAreJoined(t *testing.T) {
	const rounds = 3
	const concurrentRuns = 4
	runner := fakeRunner(t, Config{})
	spec := helperSpec(t, "success")
	for round := 0; round < rounds; round++ {
		start := make(chan struct{})
		type outcome struct {
			result RunResult
			err    error
		}
		results := make(chan outcome, concurrentRuns)
		var wg sync.WaitGroup
		for i := 0; i < concurrentRuns; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				result, err := runner.Run(context.Background(), spec, testRequest(), standardLimits())
				results <- outcome{result: result, err: err}
			}()
		}
		close(start)
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatalf("runner round %d did not join all concurrent operations", round)
		}
		for i := 0; i < concurrentRuns; i++ {
			got := <-results
			if got.err != nil || got.result.Reply == nil {
				t.Fatalf("round %d worker result: %+v err=%v", round, got.result, got.err)
			}
			assertStopped(t, got.result)
		}
	}
}
