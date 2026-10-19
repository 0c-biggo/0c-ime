//go:build !windows

package main

import (
	"errors"
	"syscall"
)

func addrInUse(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }
