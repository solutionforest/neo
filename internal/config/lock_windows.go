//go:build windows

package config

import "os"

func lockFile(f *os.File)   {}
func unlockFile(f *os.File) {}
