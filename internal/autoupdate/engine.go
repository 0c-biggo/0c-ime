package autoupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/internal/enginedir"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// ErrNeedsShell：安裝的外殼太舊，不認得引擎指標；這一版只能以完整安裝（外殼更新）套用。
var ErrNeedsShell = errors.New("installed shell does not support engine pointer")

// CoreLayout 是套件內核心檔案的位置（相對套件根目錄）。
type CoreLayout struct {
	Server string // bin/0c-server 或 0c-server.exe
	Update string // 同版 0c-update，切換後更新穩定副本用；可為空
	// Settings 是設定程式（選用）。套件沒有它、或這個平台還不出貨它時為空；舊版更新器不認得這個檔案，
	// 會略過它（只複製引擎、更新器、字典與模型），所以含設定程式的套件對舊版更新器仍然有效。
	Settings string
	// MCP 是 AI 工具用的轉接 0c-mcp（選用，#126）。Windows 切換後由它換上 bin\0c-mcp.exe 的穩定副本；
	// 舊版更新器同樣略過它。
	MCP string
}

// PrepareCore 從已核對雜湊的壓縮包準備核心版本目錄 versions/<版本>-c-<雜湊>：引擎、配對字典與神經模型。
// 每個檔案都依套件清單重新核對；完成後以該引擎 -check-data 做隔離檢查，不碰個人檔與執行中的引擎。
// scratch 是解開套件的暫存根目錄（見 update.StageIn）。
func PrepareCore(base, scratch, archive string, t Target, goos, arch string, layout CoreLayout) (string, error) {
	root, cleanup, err := update.StageIn(scratch, archive, t.SHA256, goos, arch)
	if err != nil {
		return "", err
	}
	defer cleanup()
	release, err := update.Verify(root, goos, arch)
	if err != nil {
		return "", err
	}
	if release.Version != t.Version {
		return "", fmt.Errorf("套件版本 %s 與索引 %s 不符", release.Version, t.Version)
	}
	if _, ok := release.Files[layout.Server]; !ok {
		return "", fmt.Errorf("套件缺少 %s", layout.Server)
	}
	dir := "versions/" + t.Version + "-c-" + t.SHA256[:12]
	dest := filepath.Join(base, filepath.FromSlash(dir))
	if err := checkCore(dest); err == nil {
		return dir, nil // 前一次已準備完成
	}
	os.RemoveAll(dest) // 不完整的殘留
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".core-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	var names []string
	for name := range release.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	exe := enginedir.Executable()
	copied := 0
	recorded := map[string]string{} // 最上層檔案的雜湊，供啟動前再核對
	for _, name := range names {
		var target string
		switch {
		case name == layout.Server:
			target = exe
		case layout.Update != "" && name == layout.Update:
			target = filepath.Base(layout.Update)
		case layout.Settings != "" && name == layout.Settings:
			target = enginedir.SettingsExecutable()
		case layout.MCP != "" && name == layout.MCP:
			target = filepath.Base(layout.MCP)
		case strings.HasPrefix(name, "data/"), strings.HasPrefix(name, "neural/"):
			target = name
		default:
			continue
		}
		dst := filepath.Join(stage, filepath.FromSlash(target))
		if err := copyVerified(filepath.Join(root, filepath.FromSlash(name)), dst, release.Files[name]); err != nil {
			return "", err
		}
		if !strings.Contains(target, "/") {
			recorded[target] = release.Files[name]
		}
		copied++
	}
	if copied == 0 {
		return "", fmt.Errorf("套件沒有核心檔案")
	}
	if err := enginedir.WriteFiles(stage, recorded); err != nil {
		return "", err
	}
	if out, err := exec.Command(filepath.Join(stage, exe), "-check-data", "-data", filepath.Join(stage, "data")).CombinedOutput(); err != nil {
		return "", fmt.Errorf("新版引擎隔離檢查失敗：%s (%w)", strings.TrimSpace(string(out)), err)
	}
	if err := os.Rename(stage, dest); err != nil {
		return "", err
	}
	return dir, nil
}

func checkCore(dir string) error {
	for _, p := range []string{enginedir.Executable(), "data/manifest.json"} {
		st, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("%s 不是一般檔案", p)
		}
	}
	return nil
}

func copyVerified(src, dst, want string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, st.Mode().Perm()|0o600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	got, err := update.Hash(dst)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s 複製後雜湊不符", filepath.Base(dst))
	}
	return nil
}

// SwitchPointer 把引擎指標指到新版本目錄，並在同一個檔案記下切換前的指標。
// 已經指向 dir 時不改動（切換中途被中止後重跑），保留最初記下的原指標。
func SwitchPointer(base, dir, version string) error {
	if err := checkCore(filepath.Join(base, filepath.FromSlash(dir))); err != nil {
		return err
	}
	prev, err := enginedir.Read(base)
	if err != nil {
		prev = nil // 無效的指標不值得回復
	}
	if prev != nil && prev.Directory == dir {
		return nil
	}
	if prev != nil {
		prev.Previous = nil // 只保留一層
	}
	return enginedir.Write(base, enginedir.Current{Directory: dir, Version: version, Previous: prev})
}

// RevertPointer 撤銷切換到 dir 的那一次：指標仍指向 dir 時改回當時記下的原指標（沒有就刪除指標），
// 已經不是 dir 時不做事。可以重複執行。
func RevertPointer(base, dir string) error {
	cur, err := enginedir.Read(base)
	if err != nil {
		return err
	}
	if cur == nil || cur.Directory != dir {
		return nil
	}
	if cur.Previous == nil {
		return enginedir.Remove(base)
	}
	return enginedir.Write(base, *cur.Previous)
}

// PointerVersion 回傳有效引擎指標的版本；沒有或指向不完整的目錄時為空字串。
func PointerVersion(base string) string {
	if _, _, ok := enginedir.Resolve(base); !ok {
		return ""
	}
	c, err := enginedir.Read(base)
	if err != nil || c == nil || !update.ValidVersion(c.Version) {
		return ""
	}
	return c.Version
}

// PruneCore 刪除不是目前指標、也不是 keep 的核心版本目錄（完整安裝的版本目錄由安裝器管理）。
func PruneCore(base string, keep ...string) {
	kept := map[string]bool{}
	for _, k := range keep {
		kept[k] = true
	}
	if c, err := enginedir.Read(base); err == nil && c != nil {
		kept[c.Directory] = true
	}
	entries, _ := os.ReadDir(filepath.Join(base, "versions"))
	for _, e := range entries {
		rel := "versions/" + e.Name()
		if e.IsDir() && strings.Contains(e.Name(), "-c-") && !kept[rel] {
			os.RemoveAll(filepath.Join(base, "versions", e.Name()))
		}
	}
}

// Dialer 連到執行中的引擎；沒有引擎時回錯誤。
type Dialer func(timeout time.Duration) (net.Conn, error)

// dialEngine 連上引擎，整條連線限時 5 秒。
func dialEngine(dial Dialer) (net.Conn, error) {
	conn, err := dial(2 * time.Second)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn, nil
}

// exchange 在已連上的連線送一個請求、讀回覆；引擎回報失敗時回錯誤。
func exchange(conn net.Conn, env ipc.Envelope) (ipc.Reply, error) {
	if err := ipc.WriteFrame(conn, env); err != nil {
		return ipc.Reply{}, err
	}
	var r ipc.Reply
	if err := ipc.ReadFrame(conn, &r); err != nil {
		return ipc.Reply{}, err
	}
	if !r.OK {
		return r, fmt.Errorf("%s：%s", env.Op, r.Error)
	}
	return r, nil
}

func call(dial Dialer, op string) (map[string]string, error) {
	conn, err := dialEngine(dial)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	r, err := exchange(conn, ipc.Envelope{Op: op})
	if err != nil {
		return nil, err
	}
	return r.Info, nil
}

// Health 回傳運作中引擎的非個人化狀態；連不上時 running 為 false、err 為 nil。
func Health(dial Dialer) (info map[string]string, running bool, err error) {
	conn, derr := dial(2 * time.Second)
	if derr != nil {
		return nil, false, nil
	}
	conn.Close()
	info, err = call(dial, ipc.OpHealth)
	return info, err == nil, err
}

// Drain 請引擎在沒有組字時存檔退出，等到連不上為止；仍在組字或逾時回錯誤（指標已切換，
// 引擎下一次自然重啟即為新版）。沒有引擎時回 nil。
func Drain(ctx context.Context, dial Dialer, wait time.Duration) error {
	if _, running, _ := Health(dial); !running {
		return nil
	}
	// 舊版引擎可能先關閉連線才送出回覆：EOF 視為已接受交接，以下以「名稱釋放」判斷結果。
	if _, err := call(dial, ipc.OpDrain); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	// 以「連不上」判斷退出完成：交接中的引擎關閉連線後仍要存檔，這段時間名稱仍在，
	// 此時啟動新引擎會因單一實例規則直接結束。
	deadline := time.Now().Add(wait)
	for {
		conn, err := dial(time.Second)
		if err != nil {
			return nil
		}
		conn.Close()
		if time.Now().After(deadline) {
			info, _, _ := Health(dial)
			return fmt.Errorf("引擎未在期限內交接（組字中的 session：%s）", info["composing"])
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// ErrNotLive：引擎不能即時切換這項設定（舊版引擎），要交接重啟才會套用。
var ErrNotLive = errors.New("引擎不支援即時切換")

// SetOutputSimplified 請執行中的引擎即時切換「輸出簡體字」（#169）：開一個暫時的 session 送 output_simplified 請求，
// 引擎換掉所有輸入框的轉換器並寫進設定檔，不必重啟。引擎沒有回報 output_simplified_live（舊版）或沒有處理時回
// ErrNotLive，由呼叫端改用交接重啟套用。
func SetOutputSimplified(dial Dialer, on bool) error {
	conn, err := dialEngine(dial)
	if err != nil {
		return err
	}
	defer conn.Close()
	opened, err := exchange(conn, ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
	if err != nil {
		return err
	}
	defer exchange(conn, ipc.Envelope{Op: ipc.OpClose, Session: opened.Session})
	if opened.Info["output_simplified_live"] != "true" {
		return ErrNotLive
	}
	r, err := exchange(conn, ipc.Envelope{Op: ipc.OpHandle, Session: opened.Session, Req: &proto.Request{Type: proto.OutputSimplified, On: on}})
	if err != nil {
		return err
	}
	if r.Snapshot == nil || !r.Snapshot.Handled {
		return ErrNotLive
	}
	if r.Snapshot.OutputSimplified != on { // 例如字典載入失敗
		return fmt.Errorf("引擎沒有切換：%s", strings.Join(r.Snapshot.Notices, "；"))
	}
	return nil
}

// DeferSettings keeps the apply request armed until every composition finishes.
func DeferSettings(dial Dialer) error {
	_, err := call(dial, ipc.OpApplySettings)
	return err
}
