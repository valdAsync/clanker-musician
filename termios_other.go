//go:build !(darwin || dragonfly || freebsd || netbsd || openbsd || linux)

package main

// silenceEcho is a no-op on platforms without a POSIX controlling terminal.
func silenceEcho() func() { return func() {} }
