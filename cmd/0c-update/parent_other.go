//go:build !windows

package main

import "os"

// parentWatch 回傳一個函式，回報啟動這支行程的父行程是否已經結束：父行程被強制結束時，
// 這支行程會被 init 或 subreaper 收養，父行程編號跟著改變。
func parentWatch() func() bool {
	parent := os.Getppid()
	return func() bool { return os.Getppid() != parent }
}
