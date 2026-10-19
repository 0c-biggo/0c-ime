package main

import "path/filepath"

func installedDataDir(string) string { return filepath.Join(installedApp(), "Contents/Resources/data") }
