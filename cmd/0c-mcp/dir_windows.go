package main

import (
	"os"
	"path/filepath"
)

func defaultDir() string { return filepath.Join(os.Getenv("LOCALAPPDATA"), "Zuin") }
