//go:build !windows

package config

import (
	"os"
	"syscall"
)

func lockFile(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlockFile(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
