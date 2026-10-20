//go:build !linux

package main

import "errors"

// 下載模型（#180）只在 Linux：macOS、Windows 的官網安裝包一定內建模型，status 只回報資訊。

var errModelsLinuxOnly = errors.New("只有 Linux 提供下載模型")

func checkModelRuntime(string) []string { return []string{} }
func modelDownloadDisabled(string) bool { return false }
func freeSpace(string) (uint64, bool)   { return 0, false }
func spawnModelsWorker() (int, error)   { return 0, errModelsLinuxOnly }
func terminateProcess(int) error        { return errModelsLinuxOnly }

var sendModelsNotice = func() error { return errModelsLinuxOnly }
var waitModelsNotice = func() (string, error) { return "", errModelsLinuxOnly }
