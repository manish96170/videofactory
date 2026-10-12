package resource

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestRepeatedConcurrentAcquireReleaseHasNoAccountingDrift(t *testing.T) {
	c := testConfig()
	c.MaxParallel = 8
	c.MaxHeavy = 8
	c.QueueLimit = 64
	m := mustManager(t, c)
	const workers = 24
	const iterations = 12
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			for iteration := 0; iteration < iterations; iteration++ {
				lease, err := m.Acquire(context.Background(), Request{RAMBytes: 1, CPU: .001})
				if err != nil {
					errs <- fmt.Errorf("worker %d iteration %d acquire: %w", worker, iteration, err)
					return
				}
				if err := lease.Release(); err != nil {
					errs <- fmt.Errorf("worker %d iteration %d release: %w", worker, iteration, err)
					return
				}
			}
		}(worker)
	}

	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := m.Snapshot(); got.Used != (Counters{}) || len(got.Active) != 0 || len(got.Queue) != 0 || got.Available != got.Total {
		t.Fatalf("repeated concurrent operations leaked resources: %+v", got)
	}
}
