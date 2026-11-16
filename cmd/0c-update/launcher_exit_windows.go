//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

// userCancelled：使用者在 UAC 視窗按了「否」（relaunchElevated 以 %w 包著 ERROR_CANCELLED）。
func userCancelled(err error) bool { return errors.Is(err, windows.ERROR_CANCELLED) }
