package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 設定程式下載中每秒輪詢 status：以前每次都啟動系統 Python import onnxruntime。現在結果快取，Python 或 sys.path
// 的目錄有變動（apt 裝上、移除套件）才重新檢查，最多用 10 分鐘。
func TestModelRuntimeCheckIsCached(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "models")
	python := filepath.Join(root, "python3")
	site := filepath.Join(root, "dist-packages")
	os.WriteFile(python, []byte("#!/bin/sh\n"), 0o755)
	os.Mkdir(site, 0o755)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(site, old, old)
	calls := 0
	missing := []string{"python3-onnxruntime"}
	probe := func(string) (runtimeProbe, error) {
		calls++
		return runtimeProbe{Missing: missing, Path: []string{site, filepath.Join(root, "absent")}}, nil
	}
	now := time.Now()
	for i := 0; i < 5; i++ {
		if got := cachedModelRuntime(dir, python, now.Add(time.Duration(i)*time.Second), probe); len(got) != 1 || got[0] != "python3-onnxruntime" {
			t.Fatalf("%v", got)
		}
	}
	if calls != 1 {
		t.Fatalf("輪詢 5 次檢查了 %d 次", calls)
	}
	// apt install python3-onnxruntime：dist-packages 多了一個目錄
	missing = []string{}
	if err := os.Mkdir(filepath.Join(site, "onnxruntime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := cachedModelRuntime(dir, python, now.Add(6*time.Second), probe); len(got) != 0 || calls != 2 {
		t.Fatalf("裝了套件要重新檢查：%v calls %d", got, calls)
	}
	cachedModelRuntime(dir, python, now.Add(7*time.Second), probe)
	if calls != 2 {
		t.Fatal("沒變動時用快取")
	}
	if cachedModelRuntime(dir, python, now.Add(7*time.Second+runtimeCacheTTL), probe); calls != 3 {
		t.Fatal("過期要重新檢查")
	}
	if cachedModelRuntime(dir, filepath.Join(root, "other-python"), now.Add(8*time.Second+runtimeCacheTTL), probe); calls != 4 {
		t.Fatal("換了 Python 要重新檢查")
	}
	// 檢查失敗（逾時）不記：下一次再檢查
	failing := func(string) (runtimeProbe, error) {
		calls++
		return runtimeProbe{Missing: []string{"python3-numpy", "python3-onnxruntime"}}, errors.New("timeout")
	}
	os.Remove(filepath.Join(dir, runtimeCacheFile))
	if got := cachedModelRuntime(dir, python, now, failing); len(got) != 2 {
		t.Fatalf("%v", got)
	}
	if cachedModelRuntime(dir, python, now, failing); calls != 6 {
		t.Fatalf("失敗的結果不應快取：calls %d", calls)
	}
}
