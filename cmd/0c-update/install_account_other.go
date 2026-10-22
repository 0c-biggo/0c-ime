//go:build !windows

package main

// refuseOtherAccount：只有 Windows 的安裝會因為提權換成另一個帳號（#199）；其他平台不擋。
func refuseOtherAccount() error { return nil }
