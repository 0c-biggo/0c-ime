//go:build !windows

package main

import (
	"net"
	"os"
	"path/filepath"
)

// defaultPaths 回傳資料包、個人檔與 unix socket 的預設路徑。
func defaultPaths() (data, user, sock string) {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local/share/zuin/data"),
		filepath.Join(home, ".local/share/zuin/user.json"),
		filepath.Join(runtimeDir(), "0c", "0c.sock")
}

// legacyName 是 zuin-server 時期的 socket，舊版 Fcitx 外殼仍連這裡。
func legacyName() string { return filepath.Join(runtimeDir(), "zuin", "zuin.sock") }

func runtimeDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return os.TempDir()
}

const transportName = "unix socket 路徑"

// serving 只試連，不改動任何檔案：等待個人資料鎖時反覆探測用，不能誤刪別人剛建立的 socket。
func serving(sock string) bool {
	if c, err := net.Dial("unix", sock); err == nil {
		c.Close()
		return true
	}
	return false
}

// alreadyRunning：socket 能連上表示已有 server 在跑；連不上則是殘留的檔案，移除後重新監聽。
func alreadyRunning(sock string) bool {
	if serving(sock) {
		return true
	}
	os.Remove(sock)
	return false
}

// endpoint 是本引擎建立的 socket。路徑可能被別人移除（登出時清掉執行期目錄、清理程式）或被
// 另一個引擎的 socket 取代；這時新外殼已經連不到本引擎，lost 回報 true，關閉時也不能刪掉別人的檔案。
type endpoint struct {
	net.Listener
	path string
	info os.FileInfo
}

func listen(sock string) (*endpoint, error) {
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		return nil, err
	}
	l, err := net.Listen("unix", sock)
	if err != nil {
		return nil, err
	}
	os.Chmod(sock, 0o600) // 只限本人
	// 關閉時由 Close 判斷路徑是否仍是自己的才刪除；net 套件預設依路徑名稱刪除，會刪掉取代者的 socket。
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	info, err := os.Stat(sock)
	if err != nil {
		l.Close()
		return nil, err
	}
	return &endpoint{Listener: l, path: sock, info: info}, nil
}

// lost 回報路徑已不存在或已不是本引擎的 socket。
func (e *endpoint) lost() bool {
	info, err := os.Stat(e.path)
	if err != nil {
		return os.IsNotExist(err)
	}
	return !os.SameFile(info, e.info)
}

func (e *endpoint) Close() error {
	err := e.Listener.Close()
	if !e.lost() {
		os.Remove(e.path)
	}
	return err
}
