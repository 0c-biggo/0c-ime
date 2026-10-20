package main

import "path/filepath"

func installedDataDir(string) string { return filepath.Join(installedApp(), "Contents/Resources/data") }

// activeDataDir：macOS 只有一種安裝，照 dataPackDir 的找法。
func activeDataDir(string) (string, bool) { return "", false }
