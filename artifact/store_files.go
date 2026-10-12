package artifact

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func logicalName(name string) bool {
	if name == "" || len(name) > 2048 || name == "." || path.Clean(name) != name || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
		for _, c := range part {
			if c < 32 || c == 127 {
				return false
			}
		}
	}
	return true
}

func (s *Store) checkRoot() error {
	i, err := os.Lstat(s.root)
	if err != nil {
		return ErrStoreIO
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || !os.SameFile(i, s.rootInfo) {
		return ErrUnsafePath
	}
	return nil
}

// checkedPath rejects every symlink component, including links pointing inside
// the root. All I/O paths are reconstructed from validated logical names. The
// owner must prevent external directory replacement for the operation lifetime.
func (s *Store) checkedPath(name string, directory bool) (string, error) {
	if !logicalName(name) {
		return "", ErrUnsafePath
	}
	if err := s.checkRoot(); err != nil {
		return "", err
	}
	p := s.root
	parts := strings.Split(name, "/")
	for n, part := range parts {
		p = filepath.Join(p, part)
		i, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		if i.Mode()&os.ModeSymlink != 0 {
			return "", ErrUnsafePath
		}
		if n < len(parts)-1 || directory {
			if !i.IsDir() {
				return "", ErrUnsafePath
			}
		} else if !i.Mode().IsRegular() && !i.IsDir() {
			return "", ErrUnsafePath
		}
	}
	return p, nil
}

func (s *Store) openRegular(name string) (*os.File, error) {
	p, err := s.checkedPath(name, false)
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, ErrUnsafePath
	}
	f, err := openNoFollow(p, false)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		f.Close()
		return nil, ErrUnsafePath
	}
	if _, err := s.checkedPath(name, false); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (s *Store) openDir(name string) (*os.File, error) {
	p := s.root
	if err := s.checkRoot(); err != nil {
		return nil, err
	}
	if name != "." {
		var err error
		p, err = s.checkedPath(name, true)
		if err != nil {
			return nil, err
		}
	}
	f, err := openNoFollow(p, true)
	if err != nil {
		return nil, err
	}
	if name == "." {
		info, err := f.Stat()
		if err != nil || !os.SameFile(info, s.rootInfo) {
			f.Close()
			return nil, ErrUnsafePath
		}
	}
	return f, nil
}

func (s *Store) makeDirs(name string) error {
	if !logicalName(name) {
		return ErrUnsafePath
	}
	if err := s.checkRoot(); err != nil {
		return err
	}
	current := ""
	for _, part := range strings.Split(name, "/") {
		current = path.Join(current, part)
		if err := os.Mkdir(filepath.Join(s.root, filepath.FromSlash(current)), 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if _, err := s.checkedPath(current, true); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) create(name string) (*os.File, error) {
	if !logicalName(name) {
		return nil, ErrUnsafePath
	}
	parent, err := s.checkedPath(path.Dir(name), true)
	if err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(parent, path.Base(name)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

func (s *Store) write(name string, data []byte) error {
	f, err := s.create(name)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

func (s *Store) read(ctx context.Context, name string, limit int64) ([]byte, error) {
	f, err := s.openRegular(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, ErrStoreLimit
	}
	var b strings.Builder
	_, err = copyContext(ctx, &b, f, limit)
	if err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func (s *Store) syncDir(name string) error {
	f, err := s.openDir(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Remove only our three known staging files, never recursively traverse an
// unexpected tree. The renamed published directory has a different name.
func (s *Store) removeStage(name string) {
	p, err := s.checkedPath(name, true)
	if err != nil {
		return
	}
	for _, file := range []string{"media", "render.json", "entry.json"} {
		_ = os.Remove(filepath.Join(p, file))
	}
	_ = os.Remove(p)
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader, limit int64) (int64, error) {
	buf := make([]byte, 64<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := src.Read(buf)
		if n > 0 {
			if int64(n) > limit-total {
				return total, ErrStoreLimit
			}
			if err := ctx.Err(); err != nil {
				return total, err
			}
			written, writeErr := dst.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if err == io.EOF {
			return total, ctx.Err()
		}
		if err != nil {
			return total, err
		}
	}
}
