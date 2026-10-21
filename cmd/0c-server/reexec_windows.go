package main

import "errors"

// reexec：Windows 只有官網安裝，由 0c-update 交接；不會用到。
func reexec(string) error { return errors.New("Windows 不支援在原行程換成新的執行檔") }
