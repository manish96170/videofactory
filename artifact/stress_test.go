package artifact

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestStoreRepeatedConcurrentLookupAndIdempotentPublish(t *testing.T) {
	store, request := storeFixture(t)
	want := mustPublish(t, store, request)
	const readers = 12
	const readIterations = 10
	const publishers = 4
	const publishIterations = 5
	start := make(chan struct{})
	errs := make(chan error, readers+publishers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for n := 0; n < readIterations; n++ {
				got, hit, err := store.Lookup(context.Background(), request.Identity)
				if err != nil {
					errs <- err
					return
				}
				if !hit || !reflect.DeepEqual(got, want) {
					errs <- errors.New("lookup missed or returned a changed cache entry")
					return
				}
			}
		}()
	}
	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for n := 0; n < publishIterations; n++ {
				got, err := store.Publish(context.Background(), request)
				if err != nil {
					errs <- err
					return
				}
				if !reflect.DeepEqual(got, want) {
					errs <- ErrEntryConflict
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := mustHit(t, store, request.Identity); !reflect.DeepEqual(got, want) {
		t.Fatal("concurrent repeated operations changed the published entry")
	}
	assertNoStages(t, store)
}
