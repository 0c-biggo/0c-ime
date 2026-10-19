//go:build !e2etest

// Package e2esync provides bounded synchronization points for desktop acceptance.
// Production builds do not read controls or write evidence.
package e2esync

func Pause(base, point string) error { return nil }
