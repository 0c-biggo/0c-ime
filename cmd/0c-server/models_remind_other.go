//go:build !linux

package main

import "log"

// 下載模型與提醒（#180）只在 Linux。
func newModelRemind(string, string, *log.Logger) *modelRemind { return nil }
