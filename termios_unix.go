//go:build darwin || dragonfly || freebsd || netbsd || openbsd || linux

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// silenceEcho disables terminal echo on the controlling terminal for as long as
// the TUI is on screen. Input is never the keyboard here, so keystrokes should
// not be printed; without this the line discipline echoes them and an Enter
// scrolls the interface. Canonical mode and ISIG are left untouched, so Ctrl+C
// still raises SIGINT.
//
// The returned function restores the previous terminal state. It is a no-op
// when there is no controlling terminal (e.g. under CI).
func silenceEcho() func() {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return func() {}
	}

	fd := int(tty.Fd())
	old, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		_ = tty.Close()
		return func() {}
	}

	noEcho := *old
	noEcho.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &noEcho); err != nil {
		_ = tty.Close()
		return func() {}
	}

	return func() {
		_ = unix.IoctlSetTermios(fd, ioctlSetTermios, old)
		_ = tty.Close()
	}
}
