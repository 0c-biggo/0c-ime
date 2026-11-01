//go:build windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

// shellReadyAfterInstall：外殼更新裝好不等重新登入（#253），這裡只核對收據真的是這一版、而且還是啟用中。
// 讀得到而不是這一版（裝好後又回復、解除安裝）回 (false, nil)，協調器清掉待辦；讀不到才是錯誤。
func shellReadyAfterInstall(receipt, version string) (bool, error) {
	b, err := os.ReadFile(receipt)
	if err != nil {
		return false, err
	}
	var r struct {
		Version string `json:"version"`
		Active  *bool  `json:"active"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return false, err
	}
	return r.Version == version && (r.Active == nil || *r.Active), nil
}

func (p *system) ShellReady(_ context.Context, version string) (bool, error) {
	return shellReadyAfterInstall(filepath.Join(p.base, "install-receipt.json"), version)
}
