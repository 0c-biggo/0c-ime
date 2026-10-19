//go:build !linux && !windows && !darwin

package main

func isolateResidentEngine() func() { return func() {} }
