//go:build !unix

package lock

import "os"

// Outside of Unix (development on Windows) there is no lock.
type Lock struct{}

func Acquire(dir, name string) (*Lock, error) {
	return &Lock{}, os.MkdirAll(dir, 0o700)
}

func (l *Lock) Release() {}
