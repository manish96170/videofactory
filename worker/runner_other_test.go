//go:build !darwin && !linux

package worker

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestUnsupportedBeforeWork(t *testing.T) {
	r, err := NewRunner(Config{})
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.Run(context.Background(), CommandSpec{Exe: exe}, testRequest(), Limits{RAMBytes: 1})
	if !errors.Is(err, ErrUnsupported) || !result.Stopped || result.Cleanup != nil {
		t.Fatal(result, err)
	}
}
