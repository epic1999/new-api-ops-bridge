// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func rootOwned(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == 0
}

func validateStateAncestors(state string) error {
	if !filepath.IsAbs(state) || filepath.Clean(state) != state {
		return fmt.Errorf("state must be a clean absolute path")
	}
	for path := state; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			if !info.IsDir() || !rootOwned(info) || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("state and ancestors must be real root-owned directories: %s", path)
			}
			// A root-owned sticky ancestor (e.g. /tmp) cannot replace root-owned children.
			if info.Mode().Perm()&0022 != 0 && (path == state || info.Mode()&os.ModeSticky == 0) {
				return fmt.Errorf("state ancestors must not be writable by other accounts: %s", path)
			}
		}
		if path == "/" {
			break
		}
	}
	return nil
}

func validateState(state string) error {
	if err := validateStateAncestors(state); err != nil {
		return err
	}
	info, err := os.Lstat(state)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("state must be root-only (chmod 700)")
	}
	return nil
}

// Pin every directory descriptor. A concurrent symlink replacement never follows
// a new destination; O_NONBLOCK prevents FIFO opens from occupying request slots.
func secureOpen(path string, directory bool) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("absolute path required")
	}
	current, err := os.Open("/")
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(path), "/"), "/")
	if path == "/" {
		return current, nil
	}
	for i, part := range parts {
		flags := syscall.O_RDONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
		if i < len(parts)-1 || directory {
			flags |= syscall.O_DIRECTORY
		}
		fd, openErr := syscall.Openat(int(current.Fd()), part, flags, 0)
		current.Close()
		if openErr != nil {
			return nil, openErr
		}
		current = os.NewFile(uintptr(fd), path)
	}
	return current, nil
}

func createInDirectory(dir *os.File, name string) (*os.File, error) {
	fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}
func removeInDirectory(dir *os.File, name string) { _ = syscall.Unlinkat(int(dir.Fd()), name) }
