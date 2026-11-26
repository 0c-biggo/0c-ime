//go:build !darwin

package main

import "fmt"

// checkInputSourceAfterHandover：只有 macOS 在更新交接後檢查輸入來源（#351，input_source_check_darwin.go）。
var checkInputSourceAfterHandover = func(string) {}

// inputSourceFollowUpCommand：只有 macOS 有（#348，input_source_followup_darwin.go）。
func inputSourceFollowUpCommand() error { return fmt.Errorf("input-source-followup 只用於 macOS") }
