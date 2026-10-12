//go:build darwin || linux

package checkpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSpecialFilesAreRejectedWithoutBlocking(t *testing.T) {
	for _, kind := range []string{"fifo", "directory"} {
		t.Run(kind, func(t *testing.T) {
			s, c := fixture(t)
			name := filepath.Join(s.root, fileName(c.Identity))
			var err error
			if kind == "fifo" {
				err = syscall.Mkfifo(name, 0o600)
			} else {
				err = os.Mkdir(name, 0o700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Load(context.Background(), c.Identity); !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("special file read: %v", err)
			}
			if err := s.Save(context.Background(), c); !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("special file replaced: %v", err)
			}
		})
	}
}
