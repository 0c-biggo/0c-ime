package main

import (
	"context"
	"os"
)

func isolateResidentEngine() func() {
	runtimeDir, err := os.MkdirTemp("", "0c-update-test-runtime-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_RUNTIME_DIR", runtimeDir) // dialEngine 連不到真的引擎，也就不會交接它
	runSystemdRun = func(context.Context, string, []string) ([]byte, error) { return nil, nil }
	spawnEngineDirect = func(string, ...string) error { return nil }
	return func() { os.RemoveAll(runtimeDir) }
}
