//go:build !windows

package main

func userCancelled(error) bool { return false }
