package main

import "path/filepath"

func installedDataDir(base string) string { return filepath.Join(base, "data") }
