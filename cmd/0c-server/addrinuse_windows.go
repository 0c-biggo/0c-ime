package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

// addrInUse：Windows 的 Winsock 錯誤不會對應到 syscall.EADDRINUSE。
func addrInUse(err error) bool { return errors.Is(err, windows.WSAEADDRINUSE) }
