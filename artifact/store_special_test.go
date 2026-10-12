//go:build darwin || linux

package artifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestStoreRejectsSpecialFilesWithoutBlocking(t *testing.T) {
	s, req := storeFixture(t)
	name := filepath.Join(s.root, "pipe")
	if err := syscall.Mkfifo(name, 0o600); err != nil {
		t.Fatal(err)
	}
	req.MediaPath = "pipe"
	req.Manifest.MediaPath = req.MediaPath
	if _, err := s.Publish(context.Background(), req); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("FIFO source: %v", err)
	}
	// Exercise the no-follow/nonblocking primitive directly: even a FIFO swapped
	// in after a regular-file precheck must return immediately with a safe error.
	if f, err := openNoFollow(name, false); !errors.Is(err, ErrUnsafePath) {
		if f != nil {
			f.Close()
		}
		t.Fatalf("FIFO open: %v", err)
	}
	link := filepath.Join(s.root, "link")
	if err := os.Symlink(filepath.Join(s.root, "sources", "shot.mp4"), link); err != nil {
		t.Fatal(err)
	}
	if f, err := openNoFollow(link, false); !errors.Is(err, ErrUnsafePath) {
		if f != nil {
			f.Close()
		}
		t.Fatalf("no-follow open: %v", err)
	}
}
