package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/internal/installs"
)

// checkModelRuntime 回報系統 Python 缺少的 Debian 套件名；結果快取在個人目錄（models_runtime.go），設定程式
// 每秒輪詢 status 時不會每次都啟動 Python。
func checkModelRuntime(base string) func(python string) []string {
	return func(python string) []string {
		return cachedModelRuntime(filepath.Join(base, "models"), python, time.Now(), probeModelRuntime)
	}
}

// probeModelRuntime 以系統 Python 試著 import numpy、onnxruntime（與 worker 相同），並回報它的 sys.path（快取的鍵）。
func probeModelRuntime(python string) (runtimeProbe, error) {
	const script = "import json,sys\nm=[]\nfor n in ('numpy','onnxruntime'):\n    try:\n        __import__(n)\n    except Exception:\n        m.append(n)\nprint(json.dumps({'missing':m,'path':[p for p in sys.path if p]}))\n"
	failed := runtimeProbe{Missing: []string{"python3-numpy", "python3-onnxruntime"}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, python, "-I", "-B", "-c", script).Output()
	if err != nil {
		return failed, err
	}
	var p struct {
		Missing []string `json:"missing"`
		Path    []string `json:"path"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return failed, err
	}
	r := runtimeProbe{Missing: []string{}, Path: p.Path}
	for _, n := range p.Missing {
		r.Missing = append(r.Missing, "python3-"+strings.TrimSpace(n))
	}
	return r, nil
}

// modelDownloadDisabled：引擎用的是系統安裝的字典，而 distribution.json 寫了 "model_download": false。
func modelDownloadDisabled(data string) bool {
	l := installs.Default()
	if l.Data == "" || filepath.Clean(data) != filepath.Clean(l.SystemData()) {
		return false
	}
	d, ok := l.Distribution()
	return ok && d.ModelDownloadDisabled()
}

// freeSpace 是 dir（或它最近的上層目錄）所在檔案系統的可用空間。
func freeSpace(dir string) (uint64, bool) {
	for {
		var st syscall.Statfs_t
		if err := syscall.Statfs(dir, &st); err == nil {
			return st.Bavail * uint64(st.Bsize), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return 0, false
		}
		dir = parent
	}
}

// spawnModelsWorker 以獨立的 session 啟動背景下載（設定程式讀完 download -json 的輸出就關掉管道，下載繼續）。
func spawnModelsWorker() (int, error) {
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(self, "models", "download-worker")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer devnull.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go cmd.Wait()
	return pid, nil
}

func terminateProcess(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// sendModelsNotice 送「下載模型」通知。通知服務支援按鈕時交給獨立的 notify-action 行程等使用者按下
// （與更新通知相同的做法），通知服務接受了才回傳成功；不支援按鈕時改送純文字並說明到哪裡下載。測試替換用。
var sendModelsNotice = func() error {
	conn, obj, err := notifyBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	if !supportsActions(obj) {
		n := modelsNotice
		n.Body += "請到靈犀注音設定的「模型」頁下載。"
		_, err = sendNotice(obj, n, nil)
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "models", "notify-action")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return startNoticeHelper(cmd, 15*time.Second)
}

// waitModelsNotice 顯示帶「下載模型」「不再提醒」的通知，等使用者按下（最多 12 小時）；測試替換用。
var waitModelsNotice = func() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
	defer cancel()
	return noticeWithActions(ctx, modelsNotice, modelsNoticeActions)
}
