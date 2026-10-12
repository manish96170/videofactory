//go:build darwin || linux

package review

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"testing"
)

func TestFIFORejectedWithoutBlocking(t *testing.T) {
	s := newStore(t, StoreConfig{})
	p, f := fixture()
	if err := syscall.Mkfifo(filepath.Join(s.root, fileName(f)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(context.Background(), p.ID); !errors.Is(err, ErrUnsafePath) {
		t.Fatal(err)
	}
}
