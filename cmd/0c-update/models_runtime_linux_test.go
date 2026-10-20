package main

import (
	"os/exec"
	"testing"
)

// 真的系統 Python：回報 sys.path（快取的鍵），缺的套件以 Debian 名稱列出。
func TestProbeModelRuntimeWithSystemPython(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("沒有 python3")
	}
	p, err := probeModelRuntime(python)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Path) == 0 || p.Missing == nil {
		t.Fatalf("%+v", p)
	}
	for _, m := range p.Missing {
		if m != "python3-numpy" && m != "python3-onnxruntime" {
			t.Fatalf("%+v", p)
		}
	}
}
