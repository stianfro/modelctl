package ocswitch

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const maxFileSize = 8 << 20

type snapshot struct {
	path string
	data []byte
	info os.FileInfo
}

// Refuse symlinks rather than replace a dotfile manager's link with a regular file.
func readFile(path string) (snapshot, error) {
	s := snapshot{path: path, data: []byte("{}\n")}
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("cannot read %s (use the real path for symlinks): %w", path, err)
	}
	defer f.Close()
	s.info, err = f.Stat()
	if err != nil {
		return s, err
	}
	if !s.info.Mode().IsRegular() {
		return s, fmt.Errorf("not a regular file: %s", path)
	}
	s.data, err = io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return s, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if len(s.data) > maxFileSize {
		return s, fmt.Errorf("file exceeds 8 MiB: %s", path)
	}
	return s, nil
}

func (s snapshot) unchanged() error {
	now, err := readFile(s.path)
	if err != nil {
		return err
	}
	same := (s.info == nil && now.info == nil) ||
		(s.info != nil && now.info != nil && os.SameFile(s.info, now.info) && s.info.Mode() == now.info.Mode())
	if !same || !bytes.Equal(s.data, now.data) {
		return fmt.Errorf("file changed during update; retry: %s", s.path)
	}
	return nil
}

func (s snapshot) write(data []byte, secret bool) (bool, error) {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	// Keep the lock file: removing it would let concurrent writers lock different inodes.
	lock, err := os.OpenFile(s.path+".ocswitch.lock", os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return false, fmt.Errorf("cannot lock %s: %w", s.path, err)
	}
	defer lock.Close()
	info, err := lock.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false, fmt.Errorf("not a regular lock file: %s.ocswitch.lock", s.path)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return false, fmt.Errorf("another process is updating %s; retry", s.path)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN) //nolint:errcheck // Closing also releases the lock.
	if err := s.unchanged(); err != nil {
		return false, err
	}
	mode := os.FileMode(0o600)
	if !secret && s.info != nil {
		mode = s.info.Mode().Perm()
	}
	if s.info != nil && bytes.Equal(s.data, data) && s.info.Mode().Perm() == mode {
		return false, nil
	}
	tmp, err := os.CreateTemp(dir, ".ocswitch-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(mode); err != nil {
		return false, err
	}
	if _, err := tmp.Write(data); err != nil {
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := s.unchanged(); err != nil {
		return false, err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return false, err
	}
	return true, nil
}
