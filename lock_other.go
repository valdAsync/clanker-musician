//go:build !(darwin || dragonfly || freebsd || netbsd || openbsd || linux)

package main

// lockEngine is a no-op where flock isn't available.
func lockEngine() (func(), error) {
	return func() {}, nil
}
