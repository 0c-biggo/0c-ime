package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// 錯誤代碼（status 的 last_error.code）。
const (
	CodeNetwork            = "network"
	CodeChecksum           = "checksum"
	CodeDiskFull           = "disk_full"
	CodeDictionaryMismatch = "dictionary_mismatch"
	CodeCanceled           = "canceled"
	CodePairing            = "pairing"
	CodeIO                 = "io"
)

// Error 是給人看的錯誤與它的代碼。
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// AsError 把任何錯誤歸到一個代碼（沒有分類的檔案錯誤是 io，空間不足是 disk_full）。
func AsError(err error) *Error {
	var e *Error
	switch {
	case errors.As(err, &e):
		return e
	case errors.Is(err, syscall.ENOSPC):
		return &Error{Code: CodeDiskFull, Message: "磁碟空間不足，模型沒有下載完成"}
	case errors.Is(err, context.Canceled):
		return &Error{Code: CodeCanceled, Message: "已取消下載"}
	}
	return &Error{Code: CodeIO, Message: err.Error()}
}

// Store 是個人目錄裡的模型（<個人資料目錄>/models）。
type Store struct{ Root string }

// NewStore 是個人資料目錄（~/.local/share/zuin）底下的模型目錄。
func NewStore(userDir string) Store { return Store{Root: filepath.Join(userDir, "models")} }

// WeightsDir 是一個上游 revision 的原始檔案。
func (s Store) WeightsDir(role, revision string) string {
	return filepath.Join(s.Root, "weights", role, revision)
}

// PairDir 是與一份字典配對的模型（引擎在這裡找 select、punct）。
func (s Store) PairDir(dictionary string) string { return filepath.Join(s.Root, dictionary) }

const pairedRecord = "paired.json"

type pairedInfo struct {
	Schema     int    `json:"schema"`
	Dictionary string `json:"dictionary_manifest_sha256"`
	ModelsLock string `json:"models_lock_sha256"`
}

// Inspection 是個人目錄目前的模型（不連網、不算權重的雜湊）。
type Inspection struct {
	// Paired：目前字典的配對目錄兩個模型都在；PairCurrent：而且是用目前的 models.lock 配的。
	Paired, PairCurrent bool
	// Weights：目前 models.lock 的權重都在（大小相符）。
	Weights bool
	// AnyUser：個人目錄有任何下載過的模型（權重或任一份配對）。
	AnyUser bool
}

// Need 是 status 的 need：none、download、repair（權重在，只要在本機重新配對）、redownload（上游模型換了）。
func (in Inspection) Need() string {
	switch {
	case in.Paired && in.PairCurrent:
		return "none"
	case in.Weights:
		return "repair"
	case in.AnyUser:
		return "redownload"
	}
	return "download"
}

func regular(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// Inspect 看個人目錄有什麼。
func (s Store) Inspect(l Lock, dictionary string) Inspection {
	var in Inspection
	if validHex(dictionary) {
		dir := s.PairDir(dictionary)
		in.Paired = true
		for _, role := range Roles {
			in.Paired = in.Paired && regular(filepath.Join(dir, role, "manifest.json"))
		}
		var info pairedInfo
		if b, err := os.ReadFile(filepath.Join(dir, pairedRecord)); err == nil && json.Unmarshal(b, &info) == nil {
			in.PairCurrent = info.ModelsLock == l.SHA256
		}
	}
	in.Weights = true
	for _, role := range Roles {
		m := l.Models[role]
		for _, n := range m.FileNames() {
			st, err := os.Stat(filepath.Join(s.WeightsDir(role, m.Revision), n))
			in.Weights = in.Weights && err == nil && st.Mode().IsRegular() && st.Size() == m.Files[n].Size
		}
	}
	if entries, err := os.ReadDir(filepath.Join(s.Root, "weights")); err == nil && len(entries) > 0 {
		in.AnyUser = true
	}
	if len(s.pairDirs()) > 0 {
		in.AnyUser = true
	}
	return in
}

// pairDirs 是所有配對目錄（名稱是 64 位十六進位）。
func (s Store) pairDirs() []string {
	entries, _ := os.ReadDir(s.Root)
	var out []string
	for _, e := range entries {
		if e.IsDir() && validHex(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// DefaultBaseURL 是 Hugging Face。
const DefaultBaseURL = "https://huggingface.co"

// Fetcher 從 Hugging Face（或鏡像）下載上游檔案：<BaseURL>/<repo>/resolve/<revision>/<file>。
type Fetcher struct {
	BaseURL string
	Client  *http.Client
	// Stall：這麼久收不到任何資料就當成網路中斷（預設 60 秒）。整個檔案不設時限，慢速網路也能下載完。
	Stall time.Duration
}

func (f *Fetcher) url(m Model, name string) string {
	base := strings.TrimRight(f.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	return base + "/" + m.Repository + "/resolve/" + m.Revision + "/" + name
}

// Progress 回報階段與已處理的位元組：downloading、verifying、pairing。
type Progress func(phase string, done, total int64)

// Install 讓個人目錄有一份可以用的模型：權重依 models.lock 逐檔核對（已經有的直接沿用，f 為 nil 時只用已有的，
// 也就是升級後的重新配對），在本機配對並核對 pairing.lock，最後整個配對目錄一次放上去，再清掉舊的權重與配對。
// 任何一步失敗都只刪暫存目錄，原本的模型不動。呼叫端要持有 models 的鎖。
func (s Store) Install(ctx context.Context, l Lock, p Pairing, dictionary string, f *Fetcher, progress Progress) error {
	if progress == nil {
		progress = func(string, int64, int64) {}
	}
	if !p.Matches(l, dictionary) {
		return &Error{Code: CodeDictionaryMismatch, Message: "目前安裝的字典沒有對應的模型配對清單，這份字典不能使用下載的模型"}
	}
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return err
	}
	s.removeStaging()
	staging, err := os.MkdirTemp(s.Root, ".staging-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	total, done := l.Size(), int64(0)
	progress("downloading", 0, total)
	for _, role := range Roles {
		m := l.Models[role]
		dst := filepath.Join(staging, "weights", role)
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return err
		}
		for _, n := range m.FileNames() {
			want, target := m.Files[n], filepath.Join(dst, n)
			if existing := filepath.Join(s.WeightsDir(role, m.Revision), n); checkFile(existing, want) == nil {
				if err := link(existing, target); err != nil {
					return err
				}
				done += want.Size
				progress("downloading", done, total)
				continue
			}
			if f == nil {
				return &Error{Code: CodeChecksum, Message: "本機的模型檔不完整，請重新下載"}
			}
			if err := f.fetch(ctx, f.url(m, n), target, want, func(n int64) {
				done += n
				progress("downloading", done, total)
			}); err != nil {
				return err
			}
		}
	}
	progress("verifying", total, total)
	for _, role := range Roles { // 下載時已逐檔核對；放上去之前再確認一次大小（沿用的檔案在這段時間可能被動過）
		m := l.Models[role]
		for _, n := range m.FileNames() {
			if st, err := os.Stat(filepath.Join(staging, "weights", role, n)); err != nil || st.Size() != m.Files[n].Size {
				return &Error{Code: CodeChecksum, Message: fmt.Sprintf("%s模型的 %s 與清單不符", Label(role), n)}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return AsError(err)
	}

	progress("pairing", total, total)
	pair := filepath.Join(staging, "pair")
	for _, role := range Roles {
		m, src, dst := l.Models[role], filepath.Join(staging, "weights", role), filepath.Join(pair, role)
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return err
		}
		manifest, err := os.ReadFile(filepath.Join(src, "manifest.json"))
		if err != nil {
			return err
		}
		vocab, err := os.ReadFile(filepath.Join(src, "vocab.json"))
		if err != nil {
			return err
		}
		out, err := Pair(manifest, vocab, dictionary)
		if err != nil {
			return &Error{Code: CodePairing, Message: fmt.Sprintf("%s模型無法配對：%v", Label(role), err)}
		}
		if err := checkPaired(p, role, out); err != nil {
			return err
		}
		for _, n := range m.FileNames() {
			if content, ok := out[n]; ok {
				err = os.WriteFile(filepath.Join(dst, n), content, 0o644)
			} else {
				err = link(filepath.Join(src, n), filepath.Join(dst, n))
			}
			if err != nil {
				return err
			}
		}
	}
	record, _ := json.MarshalIndent(pairedInfo{Schema: 1, Dictionary: dictionary, ModelsLock: l.SHA256}, "", "  ")
	if err := os.WriteFile(filepath.Join(pair, pairedRecord), append(record, '\n'), 0o644); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return AsError(err)
	}

	// 放上去：先權重（中途失敗時下一次只要重新配對），再整個配對目錄一次 rename。
	old := filepath.Join(staging, "old")
	if err := os.MkdirAll(old, 0o700); err != nil {
		return err
	}
	for _, role := range Roles {
		m := l.Models[role]
		final := s.WeightsDir(role, m.Revision)
		if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
			return err
		}
		if err := replaceDir(filepath.Join(staging, "weights", role), final, filepath.Join(old, "weights-"+role)); err != nil {
			return err
		}
	}
	if err := replaceDir(pair, s.PairDir(dictionary), filepath.Join(old, "pair")); err != nil {
		return err
	}
	s.cleanup(l, dictionary)
	return nil
}

// replaceDir 把 src 搬到 final；final 已存在時先搬到 aside（之後隨暫存目錄刪掉）。
func replaceDir(src, final, aside string) error {
	if _, err := os.Lstat(final); err == nil {
		if err := os.Rename(final, aside); err != nil {
			return err
		}
	}
	if err := os.Rename(src, final); err != nil {
		os.Rename(aside, final) // 盡量放回原本的
		return err
	}
	return nil
}

// cleanup 刪掉不是目前 models.lock 的權重、不是目前字典的配對目錄（模型或字典換過之後，下載或重新配對成功才刪）。
func (s Store) cleanup(l Lock, dictionary string) {
	weights := filepath.Join(s.Root, "weights")
	roles, _ := os.ReadDir(weights)
	for _, r := range roles {
		m, ok := l.Models[r.Name()]
		if !ok {
			os.RemoveAll(filepath.Join(weights, r.Name()))
			continue
		}
		revs, _ := os.ReadDir(filepath.Join(weights, r.Name()))
		for _, rev := range revs {
			if rev.Name() != m.Revision {
				os.RemoveAll(filepath.Join(weights, r.Name(), rev.Name()))
			}
		}
	}
	for _, d := range s.pairDirs() {
		if d != dictionary {
			os.RemoveAll(s.PairDir(d))
		}
	}
}

// removeStaging 刪掉之前中斷留下的暫存目錄（呼叫端持有鎖，不會是別人正在用的）。
func (s Store) removeStaging() {
	entries, _ := os.ReadDir(s.Root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") {
			os.RemoveAll(filepath.Join(s.Root, e.Name()))
		}
	}
}

// Remove 刪掉個人目錄的所有模型（權重、配對、暫存與錯誤紀錄）；提醒紀錄留著，刪了模型的人不再被提醒。
func (s Store) Remove() error {
	s.removeStaging()
	var errs []error
	for _, d := range s.pairDirs() {
		errs = append(errs, os.RemoveAll(s.PairDir(d)))
	}
	errs = append(errs, os.RemoveAll(filepath.Join(s.Root, "weights")))
	if err := os.Remove(filepath.Join(s.Root, stateFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// link 以硬連結共用權重（不多佔 260 MB）；檔案系統不支援時改用符號連結（worker 照路徑讀檔，兩種都可以）。
func link(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return os.Symlink(abs, dst)
}

// checkFile 核對大小與 SHA-256。
func checkFile(path string, want File) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() != want.Size {
		return errors.New("大小不符")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != want.SHA256 {
		return errors.New("SHA-256 不符")
	}
	return nil
}

// fetch 下載一個檔案到 target，邊下載邊算雜湊；大小或雜湊不符、中斷時回傳錯誤（target 留在暫存目錄裡，由呼叫端刪）。
func (f *Fetcher) fetch(ctx context.Context, url, target string, want File, add func(int64)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stall := f.Stall
	if stall <= 0 {
		stall = time.Minute
	}
	var stalled atomic.Bool
	timer := time.AfterFunc(stall, func() { stalled.Store(true); cancel() })
	defer timer.Stop()
	networkErr := func(err error) error {
		switch {
		case stalled.Load():
			return &Error{Code: CodeNetwork, Message: "網路連線停滯，模型沒有下載完成"}
		case errors.Is(err, context.Canceled):
			return &Error{Code: CodeCanceled, Message: "已取消下載"}
		}
		return &Error{Code: CodeNetwork, Message: "無法連到 Hugging Face：" + err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return networkErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &Error{Code: CodeNetwork, Message: fmt.Sprintf("Hugging Face 回應 HTTP %d", resp.StatusCode)}
	}
	if resp.ContentLength >= 0 && resp.ContentLength != want.Size {
		return &Error{Code: CodeChecksum, Message: "下載的檔案大小與清單不符"}
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	h := sha256.New()
	buf := make([]byte, 256<<10)
	var got int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			timer.Reset(stall)
			got += int64(n)
			if got > want.Size {
				return &Error{Code: CodeChecksum, Message: "下載的檔案大小與清單不符"}
			}
			h.Write(buf[:n])
			if _, err := out.Write(buf[:n]); err != nil {
				return AsError(err)
			}
			add(int64(n))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return networkErr(rerr)
		}
	}
	if got != want.Size {
		return &Error{Code: CodeNetwork, Message: "下載中斷，模型沒有下載完成"}
	}
	if hex.EncodeToString(h.Sum(nil)) != want.SHA256 {
		return &Error{Code: CodeChecksum, Message: "下載的檔案與清單的 SHA-256 不符"}
	}
	if err := out.Close(); err != nil {
		return AsError(err)
	}
	return nil
}
