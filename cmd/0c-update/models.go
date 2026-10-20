package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/filelock"
	"github.com/0c-biggo/0c-ime/internal/models"
	"github.com/0c-biggo/0c-ime/internal/neural"
	"github.com/0c-biggo/0c-ime/internal/settings"
	"github.com/0c-biggo/0c-ime/internal/update"
	upstream "github.com/0c-biggo/0c-ime/models"
)

// 個人目錄的神經模型（#180）：Linux 沒有內建模型的安裝（Debian 的套件）由使用者在設定程式按下載，從
// Hugging Face 取得、在本機配對。契約見 docs/settings-contract.md 的「模型」。網路只在 download 時發生；
// status 不連網。官網安裝包內建模型，只回報資訊。

const (
	modelsLockName   = "models.lock"   // base/update 下的命名鎖：下載、刪除、重新配對互斥
	modelsRemindLock = "models-remind" // remind.json 的讀改寫
	pairingLockFile  = "pairing.lock.json"
	// modelsMirrorEnv：換掉 Hugging Face 的位址（鏡像站或測試的假伺服器）。檔案照樣逐一核對 models.lock 的
	// SHA-256，換位址不會換掉內容。
	modelsMirrorEnv = "ZC_MODELS_BASE_URL"
)

// 停用下載的原因（status 的 disabled_reason）。
const (
	modelsDisabledBundled      = "bundled"             // 安裝包內建模型
	modelsDisabledPlatform     = "platform"            // 只有 Linux 提供下載
	modelsDisabledDistribution = "distribution"        // 發行版關掉（distribution.json 的 model_download）
	modelsDisabledDictionary   = "dictionary_mismatch" // 這份字典沒有配對清單（例如發行版自己重建字典）
)

// 錯誤代碼（-json 的 error.code）。
const (
	codeModelsUnavailable = "unavailable" // 這個安裝不能下載
	codeModelsBundled     = "bundled"     // 內建模型不能刪
)

type modelsRuntime struct {
	OK      bool     `json:"ok"`
	Python  string   `json:"python"`
	Missing []string `json:"missing"`
}

type modelView struct {
	Role       string `json:"role"`
	Label      string `json:"label"`
	State      string `json:"state"`
	Source     string `json:"source"`
	Version    string `json:"version"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	URL        string `json:"url"`
	License    string `json:"license"`
	// Equivalent：本機模型不是 Hugging Face 那一份檔案，但權重相同、只差匯出資訊（models/equivalent.json，
	// 0.2.2 以前官網套件內建的那份）。上游資訊照樣附上。
	Equivalent bool `json:"equivalent"`
}

type modelsOperation struct {
	Phase     string    `json:"phase"`
	DoneBytes int64     `json:"done_bytes"`
	Total     int64     `json:"total_bytes"`
	StartedAt time.Time `json:"started_at"`
}

type modelsStatus struct {
	Schema         int               `json:"schema"`
	OK             bool              `json:"ok"`
	Source         string            `json:"source"`
	CanDownload    bool              `json:"can_download"`
	DisabledReason string            `json:"disabled_reason"`
	Runtime        modelsRuntime     `json:"runtime"`
	Need           string            `json:"need"`
	DownloadSize   int64             `json:"download_size"`
	Models         []modelView       `json:"models"`
	Operation      *modelsOperation  `json:"operation"`
	LastError      *models.LastError `json:"last_error"`
	Dictionary     string            `json:"dictionary_sha256,omitempty"`
}

// modelsEnv 是 status 與各命令需要的環境；測試換成暫存目錄。
type modelsEnv struct {
	base string // 個人資料目錄（~/.local/share/zuin）
	data string // 引擎用的字典
	goos string
	lock []byte
	// equivalent 是 models/equivalent.json：與 models.lock 的 model.onnx 等價的雜湊。
	equivalent []byte
	dial       autoupdate.Dialer
	// runtimeCheck 核對系統 Python 能不能 import numpy、onnxruntime，回傳缺少的 apt 套件。
	runtimeCheck func(python string) []string
	// distributionDisabled：發行版關掉了模型下載與提醒。
	distributionDisabled func(data string) bool
	now                  func() time.Time
}

func defaultModelsEnv() modelsEnv {
	base := basePath()
	return modelsEnv{base: base, data: dataPackDir(base), goos: runtime.GOOS, lock: upstream.Lock, equivalent: upstream.Equivalent, dial: dialEngine,
		runtimeCheck: checkModelRuntime, distributionDisabled: modelDownloadDisabled, now: time.Now}
}

// newModelsEnv 測試替換用。
var newModelsEnv = defaultModelsEnv

func (e modelsEnv) store() models.Store { return models.NewStore(e.base) }

// dictionary 是字典 manifest 的 SHA-256（引擎與 worker 核對配對用的同一個值）。
func (e modelsEnv) dictionary() string {
	b, err := os.ReadFile(filepath.Join(e.data, "manifest.json"))
	if err != nil {
		return ""
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// pairing 讀字典旁的 pairing.lock.json（<data>/../neural/pairing.lock.json，隨套件出貨）。
func (e modelsEnv) pairing() (models.Pairing, error) {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Clean(e.data)), "neural", pairingLockFile))
	if err != nil {
		return models.Pairing{}, err
	}
	return models.ParsePairing(b)
}

// modelsSnapshot 是一次檢查的結果，status、remind、download 共用。
type modelsSnapshot struct {
	lock       models.Lock
	equivalent models.Equivalents
	dictionary string
	bundled    neural.Bundled
	inspect    models.Inspection
	pairing    models.Pairing
	pairingOK  bool
	source     string
}

func (e modelsEnv) snapshot() (modelsSnapshot, error) {
	var s modelsSnapshot
	l, err := models.ParseLock(e.lock)
	if err != nil {
		return s, err
	}
	s.lock, s.dictionary = l, e.dictionary()
	s.equivalent, _ = models.ParseEquivalents(e.equivalent) // 讀不到就只認 models.lock 本身（測試核對編進去的那份）
	s.bundled = neural.Find(neural.Options{DataDir: e.data, SystemPython: neural.SystemPython()})
	s.inspect = e.store().Inspect(l, s.dictionary)
	if p, err := e.pairing(); err == nil {
		s.pairing, s.pairingOK = p, p.Matches(l, s.dictionary)
	}
	switch {
	case s.bundled.SelectSource == neural.SourceBundled:
		s.source = neural.SourceBundled
	case s.inspect.Paired:
		s.source = neural.SourceUser
	default:
		s.source = "none"
	}
	return s, nil
}

func (e modelsEnv) status() (modelsStatus, error) {
	snap, err := e.snapshot()
	if err != nil {
		return modelsStatus{}, err
	}
	st := modelsStatus{Schema: contractSchema, OK: true, Source: snap.source, Need: snap.inspect.Need(), DownloadSize: snap.lock.Size(),
		Dictionary: snap.dictionary, Runtime: modelsRuntime{Missing: []string{}}}
	switch {
	case e.goos != "linux":
		st.DisabledReason = modelsDisabledPlatform
	case snap.source == neural.SourceBundled:
		st.DisabledReason = modelsDisabledBundled
	case e.distributionDisabled(e.data):
		st.DisabledReason = modelsDisabledDistribution
	case !snap.pairingOK:
		st.DisabledReason = modelsDisabledDictionary
	default:
		st.CanDownload = true
	}
	if snap.source == neural.SourceBundled {
		st.Need = "none"
	}
	st.Runtime = e.runtime(snap.bundled)

	engine := map[string]string{}
	if m := modelStatus(e.dial); m["state"] == "running" {
		engine[models.RoleSelect], _ = m["selection_state"].(string)
		engine[models.RolePunct], _ = m["punctuation_state"].(string)
	}
	for _, role := range models.Roles {
		m := snap.lock.Models[role]
		v := modelView{Role: role, Label: models.Label(role), State: engine[role], Size: m.Files["model.onnx"].Size}
		dir, source := "", ""
		bundledDir := snap.bundled.SelectPackage
		if role == models.RolePunct {
			bundledDir = snap.bundled.PunctPackage
		}
		switch {
		case snap.source == neural.SourceBundled && regularFile(filepath.Join(bundledDir, "manifest.json")):
			dir, source = bundledDir, neural.SourceBundled
		case snap.source == neural.SourceUser:
			dir, source = filepath.Join(e.store().PairDir(snap.dictionary), role), neural.SourceUser
		}
		match := models.MatchUpstream // 還沒安裝：顯示要下載的那一份
		if dir != "" {
			v.Path, v.Source = dir, source
			v.SHA256 = manifestModelSHA(dir)
			if fi, err := os.Stat(filepath.Join(dir, "model.onnx")); err == nil {
				v.Size = fi.Size()
			}
			match = snap.lock.Match(role, v.SHA256, snap.equivalent)
		}
		// 本機的模型是 models.lock 記的上游版本，或與它等價的匯出（0.2.2 以前官網套件內建的那份）時才附上
		// Hugging Face 的資訊；其他模型（例如發行版自己換的）不冒用上游的網址。
		if match != models.MatchNone {
			v.Version, v.Repository, v.Revision, v.URL, v.License = m.Version, m.Repository, m.Revision, m.URL(), m.License
			v.Equivalent = match == models.MatchEquivalent
		}
		st.Models = append(st.Models, v)
	}
	if op := e.operation(); op != nil {
		st.Operation = op
	}
	st.LastError = e.store().LastError()
	return st, nil
}

func regularFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// manifestModelSHA 是模型 manifest 記的 model.onnx 雜湊（worker 載入時核對這個值；不必為了顯示讀 130 MB）。
func manifestModelSHA(dir string) string {
	var m struct {
		Files map[string]string `json:"files"`
	}
	if b, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil && json.Unmarshal(b, &m) == nil {
		return m.Files["model.onnx"]
	}
	return ""
}

// runtime：內附 Python 不必檢查；Linux 用系統的 python3，缺什麼列出 Debian 的套件名。
func (e modelsEnv) runtime(b neural.Bundled) modelsRuntime {
	r := modelsRuntime{Missing: []string{}}
	switch {
	case b.PythonSource == neural.SourceBundled:
		r.OK, r.Python = true, b.Python
	case e.goos != "linux":
	case b.PythonSource == neural.SourceSystem:
		r.Python = b.Python
		r.Missing = e.runtimeCheck(b.Python)
		r.OK = len(r.Missing) == 0
	default: // 沒有 python3
		r.Missing = []string{"python3", "python3-numpy", "python3-onnxruntime"}
	}
	return r
}

// operation 是進行中的下載；只有寫進度的行程還握著鎖時才算。
func (e modelsEnv) operation() *modelsOperation {
	op, ok := e.store().ReadOperation()
	if !ok || !modelsLockHeldBy(e.base, op.PID) {
		return nil
	}
	return &modelsOperation{Phase: op.Phase, DoneBytes: op.DoneBytes, Total: op.Total, StartedAt: op.StartedAt}
}

func modelsLockHeldBy(base string, pid int) bool {
	b, err := os.ReadFile(filepath.Join(base, "update", modelsLockName))
	if err != nil {
		return false
	}
	var owner int
	fmt.Sscan(string(b), &owner)
	return owner == pid && filelock.ProcessAlive(pid)
}

// ---- 命令 ----

func modelsUsage() error {
	return usageError("用法：0c-update models status|download|cancel|remove [-json]；models remind [-committed]")
}

// modelsCommand 處理 0c-update models …。
func modelsCommand(args []string) (resultErr error) {
	if len(args) == 0 {
		return modelsUsage()
	}
	sub, rest := args[0], args[1:]
	asJSON := false
	for _, a := range rest {
		if isJSONFlag(a) {
			asJSON = true
		}
	}
	defer func() {
		if asJSON && resultErr != nil && !errors.Is(resultErr, errReported) {
			resultErr = failJSON(os.Stdout, resultErr)
		}
	}()
	flags := flag.NewFlagSet("models "+sub, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Bool("json", false, "")
	committed := flags.Bool("committed", false, "")
	action := flags.String("action", "", "")
	if err := flags.Parse(rest); err != nil || flags.NArg() != 0 {
		return modelsUsage()
	}
	env := newModelsEnv()
	emit := func(st modelsStatus, err error) error {
		if err != nil {
			return err
		}
		if asJSON {
			return emitJSON(os.Stdout, st)
		}
		printModels(os.Stdout, st)
		return nil
	}
	switch sub {
	case "status":
		return emit(env.status())
	case "download":
		if asJSON {
			return emit(env.startDownload())
		}
		if err := env.download(context.Background(), os.Stdout); err != nil {
			return err
		}
		return emit(env.status())
	case "download-worker": // 內部用：download -json 在背景啟動的下載
		return env.download(context.Background(), io.Discard)
	case "cancel":
		return emit(env.cancel())
	case "remove":
		return emit(env.remove())
	case "remind": // 引擎呼叫：重新配對與提醒，沒有輸出
		return env.remind(*committed)
	case "notify-action": // 內部用：提醒通知等使用者按下按鈕（-action 直接執行那個按鈕）
		if *action == "" {
			chosen, err := waitModelsNotice()
			if err != nil {
				return err
			}
			*action = chosen
		}
		return env.noticeAction(*action)
	}
	return modelsUsage()
}

func modelsError(code, message string) error { return &models.Error{Code: code, Message: message} }

// startDownload 在背景開始下載，等它記下進度（最多幾秒）就回傳 status；已經在下載時直接回傳。
func (e modelsEnv) startDownload() (modelsStatus, error) {
	st, err := e.status()
	if err != nil || st.Operation != nil {
		return st, err
	}
	if !st.CanDownload {
		return st, modelsError(codeModelsUnavailable, modelsDisabledMessage(st.DisabledReason))
	}
	pid, err := spawnModelsWorker()
	if err != nil {
		return st, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if op, ok := e.store().ReadOperation(); ok && op.PID == pid {
			break
		}
		if !filelock.ProcessAlive(pid) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return e.status()
}

func modelsDisabledMessage(reason string) string {
	switch reason {
	case modelsDisabledBundled:
		return "這個安裝已內建模型，不需要下載"
	case modelsDisabledPlatform:
		return "只有 Linux 提供下載模型"
	case modelsDisabledDistribution:
		return "發行版已關閉模型下載"
	case modelsDisabledDictionary:
		return "目前安裝的字典沒有對應的模型配對清單，不能使用下載的模型"
	}
	return "目前不能下載模型"
}

// download 在前景下載、核對、配對，完成後請引擎交接重新載入。下載中途的進度寫進 operation.json 供 status 輪詢。
func (e modelsEnv) download(ctx context.Context, out io.Writer) error {
	st, err := e.status()
	if err != nil {
		return err
	}
	if !st.CanDownload {
		return modelsError(codeModelsUnavailable, modelsDisabledMessage(st.DisabledReason))
	}
	unlock, err := filelock.Acquire(e.base, modelsLockName)
	if err != nil {
		if errors.Is(err, filelock.ErrLocked) {
			return modelsError(codeInProgress, "模型正在下載")
		}
		return err
	}
	defer unlock()
	store := e.store()
	snap, err := e.snapshot()
	if err != nil {
		return err
	}
	store.SetLastError(nil, e.now())
	op := models.Operation{PID: os.Getpid(), Phase: "downloading", Total: snap.lock.Size(), StartedAt: e.now().UTC().Truncate(time.Second)}
	store.WriteOperation(op)
	defer store.ClearOperation()

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT) // cancel 送 SIGTERM
	defer stop()
	fail := func(err error) error {
		store.SetLastError(err, e.now())
		return models.AsError(err)
	}
	if !snap.inspect.Weights {
		if free, ok := freeSpace(store.Root); ok && free < uint64(snap.lock.Size())+16<<20 {
			return fail(modelsError(models.CodeDiskFull, fmt.Sprintf("磁碟空間不足：模型約需 %d MB", mb(snap.lock.Size()))))
		}
	}
	lockFile := filepath.Join(e.base, "update", modelsLockName)
	last := time.Time{}
	progress := func(phase string, done, total int64) {
		now := time.Now()
		if phase == op.Phase && now.Sub(last) < 250*time.Millisecond && done < total {
			return
		}
		last = now
		op.Phase, op.DoneBytes, op.Total = phase, done, total
		store.WriteOperation(op)
		os.Chtimes(lockFile, now, now) // 鎖超過一小時會被當成殘留；慢速下載時持續更新
		if out != io.Discard {
			fmt.Fprintf(out, "\r%s %d／%d MB", phaseLabel(phase), mb(done), mb(total))
		}
	}
	client := update.Client()
	client.Timeout = 0 // 單一檔案 130 MB；以停滯偵測取代整體時限
	fetcher := &models.Fetcher{BaseURL: os.Getenv(modelsMirrorEnv), Client: client}
	err = store.Install(ctx, snap.lock, snap.pairing, snap.dictionary, fetcher, progress)
	if out != io.Discard {
		fmt.Fprintln(out)
	}
	if err != nil {
		return fail(err)
	}
	e.editRemind(func(r *models.Remind) { r.Stop(models.StopDownloaded, e.now()) })
	reloadEngineForModels()
	return nil
}

// mb 以十進位 MB 顯示（與 #180、設定程式寫的「約 260 MB」一致）。
func mb(n int64) int64 { return (n + 500_000) / 1_000_000 }

func phaseLabel(phase string) string {
	switch phase {
	case "verifying":
		return "核對中"
	case "pairing":
		return "配對中"
	}
	return "下載中"
}

// reloadEngineForModels 請引擎在沒有組字時交接重啟（同 set -apply），新引擎依 neural.Find 載入個人目錄的模型。
var reloadEngineForModels = func() { applySettings(dialEngine, func(string) {}) }

// cancel 取消進行中的下載：送 SIGTERM 給下載的行程，等它刪掉暫存檔、放開鎖。
func (e modelsEnv) cancel() (modelsStatus, error) {
	op, ok := e.store().ReadOperation()
	if ok && modelsLockHeldBy(e.base, op.PID) {
		if err := terminateProcess(op.PID); err != nil {
			return modelsStatus{}, err
		}
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) && modelsLockHeldBy(e.base, op.PID) {
			time.Sleep(50 * time.Millisecond)
		}
	}
	return e.status()
}

// remove 刪掉個人目錄的模型，請引擎交接回到統計選字。
func (e modelsEnv) remove() (modelsStatus, error) {
	st, err := e.status()
	if err != nil {
		return st, err
	}
	if st.Operation != nil {
		return st, modelsError(codeInProgress, "模型正在下載，請先取消")
	}
	if st.Source == neural.SourceBundled {
		return st, modelsError(codeModelsBundled, "內建的模型隨安裝包提供，不能在這裡刪除")
	}
	unlock, err := filelock.Acquire(e.base, modelsLockName)
	if err != nil {
		if errors.Is(err, filelock.ErrLocked) {
			return st, modelsError(codeInProgress, "模型正在下載，請先取消")
		}
		return st, err
	}
	err = e.store().Remove()
	unlock()
	if err != nil {
		return st, err
	}
	if st.Source == neural.SourceUser {
		reloadEngineForModels()
	}
	return e.status()
}

func (e modelsEnv) editRemind(fn func(*models.Remind)) error {
	return filelock.With(e.base, modelsRemindLock, func() error {
		s := e.store()
		r := s.LoadRemind()
		fn(&r)
		return s.SaveRemind(r)
	})
}

// remind 由引擎呼叫（啟動時、之後送出字又沒在組字時最多每小時一次）：
//  1. 升級換了字典、模型沒換：用已有的權重在本機重新配對，不連網，完成後請引擎交接載入；
//  2. 沒有模型時依 remind.json 的排程送「下載模型」通知（最多 3 次、間隔 24 小時）。
func (e modelsEnv) remind(committed bool) error {
	if e.goos != "linux" {
		return nil
	}
	snap, err := e.snapshot()
	if err != nil {
		return err
	}
	repairFailed := ""
	if snap.source == "none" && snap.inspect.AnyUser {
		if snap.inspect.Weights && snap.pairingOK {
			if err := e.repair(snap); err != nil {
				fmt.Fprintln(os.Stderr, "0c-update: 模型重新配對失敗：", err)
				repairFailed = snap.dictionary
			} else if snap, err = e.snapshot(); err != nil {
				return err
			}
		} else {
			repairFailed = snap.dictionary // 模型換了（要重新下載）或字典沒有配對清單：由設定頁說明，不一直重試
		}
	}
	neuralSelection := true
	if r, err := settings.Read(settings.Path(filepath.Join(e.base, "user.json"))); err == nil {
		neuralSelection = r.Settings.Selection == settings.Neural
	}
	return e.editRemind(func(r *models.Remind) {
		if repairFailed != "" {
			r.RepairFailed = repairFailed
		}
		now := e.now()
		send := r.Decide(models.RemindInput{Now: now, Committed: committed, HasModel: snap.source == neural.SourceBundled,
			Downloaded: snap.inspect.AnyUser, Neural: neuralSelection,
			Disabled: e.distributionDisabled(e.data) || !snap.pairingOK})
		if !send {
			return
		}
		if err := sendModelsNotice(); err != nil {
			fmt.Fprintln(os.Stderr, "0c-update: 無法顯示下載模型的提醒：", err)
			r.NotDelivered(now)
			return
		}
		r.Sent(now)
	})
}

// repair 在本機重新配對（不連網）；成功後請引擎交接。
func (e modelsEnv) repair(snap modelsSnapshot) error {
	unlock, err := filelock.Acquire(e.base, modelsLockName)
	if err != nil {
		return err
	}
	defer unlock()
	store := e.store()
	if err := store.Install(context.Background(), snap.lock, snap.pairing, snap.dictionary, nil, nil); err != nil {
		store.SetLastError(err, e.now())
		return err
	}
	store.SetLastError(nil, e.now())
	reloadEngineForModels()
	return nil
}

// 提醒通知的文字與按鈕（#180）。
var modelsNotice = autoupdate.Notice{
	Title: "靈犀注音：下載選字模型",
	Body:  "目前使用統計選字。下載神經選字與補標點模型（約 260 MB）後，選字更準確，也能自動補標點。",
}

const (
	modelsActionDownload = "download-models"
	modelsActionDismiss  = "dismiss"
)

var modelsNoticeActions = []string{modelsActionDownload, "下載模型", modelsActionDismiss, "不再提醒"}

// noticeAction 處理通知的按鈕：「下載模型」開設定程式的模型頁（下載仍要使用者在那裡確認），「不再提醒」永久停止。
func (e modelsEnv) noticeAction(action string) error {
	switch action {
	case modelsActionDownload:
		return openSettingsPage("models")
	case modelsActionDismiss:
		return e.editRemind(func(r *models.Remind) { r.Stop(models.StopDismissed, e.now()) })
	case "":
		return nil
	}
	return modelsUsage()
}

// openSettingsPage 測試替換用。
var openSettingsPage = func(page string) error { return openSettings([]string{page}) }

func printModels(w io.Writer, st modelsStatus) {
	switch st.Source {
	case neural.SourceBundled:
		fmt.Fprintln(w, "神經模型：安裝包內建")
	case neural.SourceUser:
		fmt.Fprintln(w, "神經模型：個人目錄（從 Hugging Face 下載）")
	default:
		fmt.Fprintln(w, "神經模型：未安裝，使用統計選字")
	}
	for _, m := range st.Models {
		line := fmt.Sprintf("%s：", m.Label)
		if m.Path != "" {
			line += m.Path
		} else {
			line += "未安裝"
		}
		if m.URL != "" {
			line += "（" + m.URL + "，" + m.License + "）"
		}
		if m.Equivalent {
			line += "；權重與公開版相同，匯出資訊不同"
		}
		fmt.Fprintln(w, line)
	}
	if st.Operation != nil {
		fmt.Fprintf(w, "%s：%d／%d MB\n", phaseLabel(st.Operation.Phase), mb(st.Operation.DoneBytes), mb(st.Operation.Total))
	}
	switch {
	case st.CanDownload && st.Need == "download":
		fmt.Fprintf(w, "可以下載（約 %d MB）：0c-update models download\n", mb(st.DownloadSize))
	case st.CanDownload && st.Need == "redownload":
		fmt.Fprintln(w, "上游模型已更新，請重新下載：0c-update models download")
	case st.Need == "repair":
		fmt.Fprintln(w, "字典已更新，下次引擎啟動時會在本機重新配對")
	case !st.CanDownload && st.DisabledReason != modelsDisabledBundled:
		fmt.Fprintln(w, modelsDisabledMessage(st.DisabledReason))
	}
	if len(st.Runtime.Missing) > 0 {
		fmt.Fprintf(w, "缺少推論元件，請安裝：sudo apt install %s\n", strings.Join(st.Runtime.Missing, " "))
	}
	if st.LastError != nil {
		fmt.Fprintf(w, "上一次失敗：%s\n", st.LastError.Message)
	}
}
