package main

import (
	"os"
	"path/filepath"
)

func defaultDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local/share/zuin")
}
