//go:build darwin || dragonfly || freebsd || netbsd || openbsd || linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

var errEngineBusy = errors.New("already playing in another session")

// lockEngine makes sure only one engine plays at a time. The OS drops the lock
// when the process exits, so a crash never leaves it stuck.
func lockEngine() (func(), error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "clanker-musician")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "engine.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errEngineBusy
		}
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
