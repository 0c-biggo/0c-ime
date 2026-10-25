package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/models"
	"github.com/0c-biggo/0c-ime/internal/settings"
)

// contractSchema 是機器可讀輸出（settings、set、check、status 的 -json）的版本。欄位只增不改；
// 使用者介面忽略不認得的欄位，schema 比自己支援的新時只顯示它認得的部分。格式見 docs/settings-contract.md。
const contractSchema = 1

// 錯誤代碼；設定相關的 unknown_key、invalid_value 來自 settings.SetError。
const (
	codeUsage      = "usage"
	codeReadOnly   = "read_only"
	codeCorrupt    = "corrupt"
	codeBusy       = "busy"
	codeInProgress = "in_progress"
	codeIO         = "io"
	// codePackageManaged：由系統套件管理員更新（#181），更新、安裝、回復與排程的命令不能用。
	codePackageManaged = "package_managed"
)

// 套用結果（apply.state）。設定「已儲存」與「已套用」是兩回事：引擎只在啟動時讀設定。
const (
	applyNotRequested  = "not_requested"      // 沒有要求套用：下次引擎啟動時生效
	applyApplied       = "applied"            // 引擎已交接退出，下一個按鍵以新設定重新啟動
	applyEngineStopped = "engine_not_running" // 引擎沒有在執行：外殼下一個按鍵啟動引擎時就是新設定
	applyDeferred      = "deferred_composing" // 仍在組字：新版引擎會在所有組字完成後套用
	applyTimeout       = "timeout"
	applyFailed        = "failed"
)

// usageError 是命令列用法錯誤。
type usageError string

func (e usageError) Error() string { return string(e) }

// drainWait 是等引擎在沒有組字時交接退出的上限；測試會縮短它。
var drainWait = 40 * time.Second

// errReported：錯誤已經以 JSON 輸出，main 只要以非零結束碼離開，不再印第二份訊息。
var errReported = errors.New("error reported as JSON")

type contractError struct {
	Code    string `json:"code"`
	Key     string `json:"key,omitempty"`
	Message string `json:"message"`
}

func classify(err error) contractError {
	var se *settings.SetError
	var ue usageError
	var me *models.Error
	switch {
	case errors.As(err, &ue):
		return contractError{Code: codeUsage, Message: err.Error()}
	case errors.As(err, &se):
		return contractError{Code: se.Code, Key: se.Key, Message: se.Msg}
	case errors.Is(err, settings.ErrReadOnly):
		return contractError{Code: codeReadOnly, Message: err.Error()}
	case errors.Is(err, settings.ErrCorrupt):
		return contractError{Code: codeCorrupt, Message: err.Error() + "；可加 -repair 把壞檔改名保留後重新設定"}
	case errors.Is(err, autoupdate.ErrUpdateInProgress):
		return contractError{Code: codeInProgress, Message: err.Error()}
	case errors.Is(err, autoupdate.ErrLocked):
		return contractError{Code: codeBusy, Message: err.Error()}
	case errors.Is(err, errPackageManaged):
		return contractError{Code: codePackageManaged, Message: err.Error()}
	case errors.As(err, &me):
		return contractError{Code: me.Code, Message: me.Message}
	}
	return contractError{Code: codeIO, Message: err.Error()}
}

func emitJSON(w io.Writer, v any) error { return json.NewEncoder(w).Encode(v) }

// failJSON 以契約格式輸出錯誤；回傳 errReported 讓 main 以非零結束碼離開。
func failJSON(w io.Writer, err error) error {
	_ = emitJSON(w, struct {
		Schema int           `json:"schema"`
		OK     bool          `json:"ok"`
		Error  contractError `json:"error"`
	}{contractSchema, false, classify(err)})
	return errReported
}

type fileView struct {
	Path      string   `json:"path"`
	WritePath string   `json:"write_path,omitempty"` // 讀取舊檔時，下一次修改寫入的新位置
	State     string   `json:"state"`                // ok、missing、corrupt、unreadable
	Schema    int      `json:"schema"`
	ReadOnly  bool     `json:"read_only"` // 檔案來自較新版本：可讀不可改
	Notes     []string `json:"notes"`
	Error     string   `json:"error,omitempty"`
}

type engineView struct {
	State   string `json:"state"` // running、not_running、unavailable
	Version string `json:"version,omitempty"`
}

// settingsItem 是一項設定：契約的描述與設定檔中的值，加上執行中的引擎實際載入的值。
// Running 為 null 表示無法得知（引擎沒執行，或舊版引擎沒有回報這一項）。
type settingsItem struct {
	settings.Item
	Running *string `json:"running"`
	Pending bool    `json:"pending"` // 已儲存的值與引擎載入的值不同：套用後才生效
}

type settingsDoc struct {
	Profiles       settings.ProfileView `json:"profiles"`
	Schema         int                  `json:"schema"`
	OK             bool                 `json:"ok"`
	File           fileView             `json:"file"`
	Engine         engineView           `json:"engine"`
	Items          []settingsItem       `json:"items"`
	PendingRestart bool                 `json:"pending_restart"`
	Features       features             `json:"features"`
}

// features 是 0.2.0 的介面入口（#128）。開發期間由內部開關控制，0.2.0 起恆為 true；設定程式與外殼仍依此
// 決定顯示與否（它們也要能搭配還沒有這些功能的舊版 0c-update），舊版介面忽略這個欄位。
type features struct {
	Vocab bool `json:"vocab"` // 0.2.0：「我的詞彙」頁、選單入口、我的片語
	// 引擎服務（#120，只在 status -json）：是否常駐（0.2.0 起恆為 true）、定義是否存在（installed／absent）與服務管理員的狀態。
	EngineService      bool   `json:"engine_service,omitempty"`
	EngineServiceState string `json:"engine_service_state,omitempty"`
	EngineServiceUnit  string `json:"engine_service_unit,omitempty"`
}

func featuresOf() features { return features{Vocab: true} }

// runningValues 把引擎 health 回報的啟動配置換成設定的命令列寫法；沒有回報的項目不出現。
func runningValues(info map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"candidate-cursor", "candidate-layout", "emoji-shortcut", "keyboard-layout", "toggle-key", "mode-memory", "start-mode", "selection-keys"} {
		if value := info[strings.ReplaceAll(key, "-", "_")]; value != "" {
			out[key] = value
		}
	}
	if v := info["selection_setting"]; v != "" {
		out["selection"] = v
	}
	switch info["document_context"] {
	case "true":
		out["document-context"] = "on"
	case "false":
		out["document-context"] = "off"
	}
	switch info["left_shift"] {
	case "true":
		out["left-shift"] = "on"
	case "false":
		out["left-shift"] = "off"
	}
	switch info["esc_clear"] {
	case "true":
		out["esc-clear"] = "on"
	case "false":
		out["esc-clear"] = "off"
	}
	switch info["advance_after_select"] {
	case "true":
		out["advance-after-select"] = "on"
	case "false":
		out["advance-after-select"] = "off"
	}
	switch info["output_simplified"] {
	case "true":
		out["output-simplified"] = "on"
	case "false":
		out["output-simplified"] = "off"
	}
	switch info["simplified_hotkey"] { // #192
	case "true":
		out["simplified-hotkey"] = "on"
	case "false":
		out["simplified-hotkey"] = "off"
	}
	switch info["ctrl_punctuation"] {
	case "true":
		out["ctrl-punctuation"] = "on"
	case "false":
		out["ctrl-punctuation"] = "off"
	}
	switch info["caps_lock_english"] { // #173
	case "true":
		out["caps-lock-english"] = "on"
	case "false":
		out["caps-lock-english"] = "off"
	}
	switch info["toneless"] { // #150
	case "true":
		out["toneless"] = "on"
	case "false":
		out["toneless"] = "off"
	}
	switch info["space_selects"] {
	case "true":
		out["space-selects"] = "on"
	case "false":
		out["space-selects"] = "off"
	}
	switch info["english_autocomplete"] {
	case "true":
		out["english-autocomplete"] = "on"
	case "false":
		out["english-autocomplete"] = "off"
	}
	switch info["mixed_input"] {
	case "true":
		out["mixed-input"] = "on"
	case "false":
		out["mixed-input"] = "off"
	}
	switch info["emoji_equals_first"] {
	case "true":
		out["emoji-equals-first"] = "on"
	case "false":
		out["emoji-equals-first"] = "off"
	}
	// 補標點只有模型狀態：依設定關閉時為 disabled_by_setting，其他狀態都表示設定為開。
	if v := info["punctuation_state"]; v != "" {
		out["punctuation"] = "on"
		if v == "disabled_by_setting" {
			out["punctuation"] = "off"
		}
	}
	if key, ok := info["fuzzy"]; ok { // 欄位存在但為空字串：全部關閉
		on := map[string]string{}
		for _, part := range strings.Split(key, ",") {
			if id, mode, found := strings.Cut(part, "="); found {
				on[id] = mode
			}
		}
		for _, p := range fuzzy.Pairs() {
			mode := "off"
			if m, found := on[p.ID()]; found {
				mode = m
			}
			out["fuzzy-"+p.ID()] = mode
		}
	}
	return out
}

func buildSettingsDoc(base string, dial autoupdate.Dialer) settingsDoc {
	path := settings.Path(filepath.Join(base, "user.json"))
	r, err := settings.Read(path)
	file := fileView{Path: r.Path, State: r.State, Schema: r.Schema, ReadOnly: r.ReadOnly, Notes: append([]string{}, r.Notes...)}
	if r.Path != path {
		file.WritePath = path
	}
	if err != nil {
		file.Error = err.Error()
	}
	doc := settingsDoc{Schema: contractSchema, OK: true, File: file, Engine: engineView{State: "not_running"}, Features: featuresOf()}
	var running map[string]string
	info, up, herr := autoupdate.Health(dial)
	switch {
	case herr != nil:
		doc.Engine.State = "unavailable"
	case up:
		doc.Engine = engineView{State: "running", Version: info["version"]}
		running = runningValues(info)
	}
	doc.Profiles = r.Settings.Profiles()
	if up && herr == nil {
		doc.Profiles.Running = info["input_profile"]
		doc.Profiles.Pending = info["input_profile_fingerprint"] != "" && info["input_profile_fingerprint"] != r.Settings.ProfileFingerprint()
		doc.PendingRestart = doc.Profiles.Pending
	}
	for _, it := range r.Settings.Items() {
		item := settingsItem{Item: it}
		if v, ok := running[it.Key]; ok {
			item.Running = &v
			want := it.Value
			if it.Effective != "" { // 停用期間實際用的值（#184）
				want = it.Effective
			}
			item.Pending = v != want
		}
		doc.PendingRestart = doc.PendingRestart || item.Pending
		doc.Items = append(doc.Items, item)
	}
	return doc
}

// setOptions 是 set 命令的參數：一到多組 鍵 值，加上旗標。
type setOptions struct {
	pairs  [][2]string
	apply  bool // 請引擎在沒有組字時交接，下一個按鍵以新設定啟動
	notify bool
	json   bool
	repair bool // 設定檔壞掉時改名保留，從預設值重新開始
}

const setUsage = "用法：0c-update set selection neural|statistical、set punctuation on|off、set document-context on|off、set mixed-input on|off、set english-autocomplete on|off、set left-shift on|off、set esc-clear on|off、set space-selects on|off、set candidate-cursor before|after、set advance-after-select on|off、set output-simplified on|off、set simplified-hotkey on|off、set ctrl-punctuation on|off、set caps-lock-english on|off（Windows、Linux）、set toneless on|off、set keyboard-layout standard|eten|hsu、set fuzzy-z-zh|fuzzy-c-ch|fuzzy-s-sh|fuzzy-en-eng off|select|auto [鍵 值 ...] [-apply] [-notify] [-json] [-repair]"

func parseSetArgs(args []string) (setOptions, error) {
	var o setOptions
	var positional, legacy []string
	for _, a := range args {
		switch {
		case a == "-json" || a == "--json":
			if o.json {
				return o, usageError("未知或重複設定參數：" + a)
			}
			o.json = true
		case a == "-repair" || a == "--repair":
			if o.repair {
				return o, usageError("未知或重複設定參數：" + a)
			}
			o.repair = true
		case strings.HasPrefix(a, "-"):
			legacy = append(legacy, a)
		default:
			positional = append(positional, a)
		}
	}
	var err error
	if o.apply, o.notify, err = configureFlags(legacy); err != nil {
		return o, usageError(err.Error())
	}
	if len(positional) < 2 || len(positional)%2 != 0 {
		return o, usageError(setUsage)
	}
	for i := 0; i < len(positional); i += 2 {
		o.pairs = append(o.pairs, [2]string{positional[i], positional[i+1]})
	}
	return o, nil
}

type changeView struct {
	Key  string `json:"key"`
	From string `json:"from"`
	To   string `json:"to"`
}

type applyView struct {
	Requested bool   `json:"requested"`
	State     string `json:"state"`
	Detail    string `json:"detail,omitempty"`
}

type setResult struct {
	Schema      int          `json:"schema"`
	OK          bool         `json:"ok"`
	Changes     []changeView `json:"changes"`
	Quarantined string       `json:"quarantined,omitempty"` // -repair 時壞檔改名保留的位置
	Apply       applyView    `json:"apply"`
}

// runSet 在寫入鎖內讀、改、存設定；全部的鍵值都通過檢查才會寫檔（要嘛全改、要嘛都不改）。
func runSet(base string, dial autoupdate.Dialer, o setOptions, notice func(string)) (setResult, error) {
	path := settings.Path(filepath.Join(base, "user.json"))
	res := setResult{Schema: contractSchema, OK: true, Changes: []changeView{}, Apply: applyView{State: applyNotRequested}}
	err := autoupdate.WithLock(base, "settings-write.lock", func() error {
		r, err := settings.Read(path)
		source := r.Path
		quarantine := false
		if err != nil {
			if !o.repair || !errors.Is(err, settings.ErrCorrupt) {
				return err
			}
			r, quarantine = settings.Report{Settings: settings.Default(), State: settings.StateMissing}, true
		}
		s := r.Settings
		if s.ReadOnly() {
			return settings.ErrReadOnly
		}
		for _, p := range o.pairs {
			from, _ := s.Value(p[0])
			if err := s.Set(p[0], p[1]); err != nil {
				return err
			}
			to, _ := s.Value(p[0])
			res.Changes = append(res.Changes, changeView{Key: p[0], From: from, To: to})
		}
		if quarantine { // 檢查都過了才動壞檔，避免輸入有誤時白白搬走它
			if res.Quarantined, err = settings.Quarantine(source, time.Now()); err != nil {
				return err
			}
		}
		if err := settings.Save(path, s); err != nil {
			return err
		}
		if choosesToggleKey(runtime.GOOS, o.pairs) {
			// 自己選了中英切換鍵：新版引擎啟動時不再把右 Shift 換成左右 Shift（#206）。
			if err := settings.MarkBothShift(path); err != nil {
				fmt.Fprintln(os.Stderr, "0c-update: 無法記下中英切換鍵的選擇：", err)
			}
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	if o.apply {
		if on, ok := liveSimplified(res.Changes); ok && autoupdate.SetOutputSimplified(dial, on) == nil {
			res.Apply = applyView{Requested: true, State: applyApplied}
			return res, nil
		}
		res.Apply = applySettings(dial, notice)
		ensureEngineService(base) // 引擎交接退出後立刻叫起常駐引擎（#120）
	}
	return res, nil
}

// choosesToggleKey：Windows 上這次設定了中英切換鍵（新鍵 toggle-key 或舊鍵 left-shift，#206）。
func choosesToggleKey(goos string, pairs [][2]string) bool {
	return goos == "windows" && slices.ContainsFunc(pairs, func(p [2]string) bool { return p[0] == "toggle-key" || p[0] == "left-shift" })
}

// liveSimplified：這次只改了「輸出簡體字」，引擎可以即時切換、不必交接重啟（#169，設定程式的開關與舊外殼的選單都走這裡）。
// 舊引擎、引擎沒在執行或切換失敗時，呼叫端照舊以交接重啟套用。
func liveSimplified(changes []changeView) (on, ok bool) {
	if len(changes) == 0 {
		return false, false
	}
	for _, c := range changes {
		if c.Key != "output-simplified" {
			return false, false
		}
	}
	return changes[len(changes)-1].To == "on", true
}

// applySettings 請引擎在沒有組字時存檔退出；外殼的下一個按鍵會以新設定重新啟動引擎。
// 設定此時已儲存：套用失敗不是設定失敗，結果以 State 說明。
func applySettings(dial autoupdate.Dialer, notice func(string)) applyView {
	v := applyView{Requested: true}
	info, running, err := autoupdate.Health(dial)
	if err != nil {
		v.State, v.Detail = applyFailed, err.Error()
		return v
	}
	if !running {
		v.State = applyEngineStopped
		restartResidentEngine()
		return v
	}
	if info["composing"] != "0" && info["settings_deferred_apply"] == "true" {
		if err := autoupdate.DeferSettings(dial); err != nil {
			return applyView{Requested: true, State: applyFailed, Detail: err.Error()}
		}
		return applyView{Requested: true, State: applyDeferred, Detail: "所有輸入框完成組字後自動套用"}
	}
	if info["composing"] != "0" {
		notice("設定已儲存；請先完成組字，完成後會切換設定。")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err = autoupdate.Drain(ctx, dial, drainWait)
	if err == nil {
		v.State = applyApplied
		restartResidentEngine()
		return v
	}
	v.Detail = err.Error()
	switch after, up, _ := autoupdate.Health(dial); {
	case up && after["composing"] != "0" && after["composing"] != "":
		v.State = applyDeferred
	case errors.Is(err, context.DeadlineExceeded):
		v.State = applyTimeout
	default:
		v.State = applyFailed
	}
	return v
}

// printSetText 保留既有的文字輸出（外殼的選單與齒輪頁據此判斷「下一個按鍵起生效」）。
func printSetText(w io.Writer, res setResult, notice func(string)) {
	parts := make([]string, 0, len(res.Changes))
	for _, c := range res.Changes {
		parts = append(parts, c.Key+"="+c.To)
	}
	list := strings.Join(parts, " ")
	if res.Quarantined != "" {
		fmt.Fprintf(w, "原設定檔無法解析，已改名保留為 %s。\n", res.Quarantined)
	}
	switch res.Apply.State {
	case applyApplied, applyEngineStopped:
		fmt.Fprintf(w, "已設定 %s，下一個按鍵起生效。\n", list)
		notice("設定已儲存，下一個按鍵起生效。")
	default:
		fmt.Fprintf(w, "已設定 %s。%s\n", list, restartHint)
		notice("設定已儲存，但尚未套用；請完成組字後登出再登入。")
	}
}

// describeUpdate 把更新查詢結果寫成一句人看的話；不把「無法檢查」或「還沒檢查」說成「已是最新」。
func describeUpdate(v autoupdate.UpdateView) string {
	at := func(t *time.Time) string {
		if t == nil {
			return "無紀錄"
		}
		return t.Local().Format("2006-01-02 15:04")
	}
	switch v.State {
	case autoupdate.CheckNever:
		return "尚未檢查更新。"
	case autoupdate.CheckUpdateAvailable:
		return fmt.Sprintf("有新版 %s。", v.Target.Version)
	case autoupdate.CheckFailed:
		return fmt.Sprintf("目前無法連線更新伺服器（%s），目前版本仍可使用；上次成功查詢：%s。", v.Error, at(v.LastSuccess))
	}
	text := fmt.Sprintf("已是此通道目前可用的版本 %s（%s 成功查詢）。", v.Installed, at(v.LastSuccess))
	if l := v.Latest; l != nil && !l.Eligible && l.Reason != "" {
		why := map[string]string{
			autoupdate.ReasonRollout:   "分批發布還沒輪到這台電腦",
			autoupdate.ReasonNoAsset:   "沒有這個平台的安裝包",
			autoupdate.ReasonWithdrawn: "該版本已撤回",
			autoupdate.ReasonFailed:    "該版本在這台電腦上健康檢查失敗過，已暫停",
		}[l.Reason]
		text += fmt.Sprintf("索引上有 %s，但%s。", l.Version, why)
	}
	return text
}

type checkResult struct {
	Schema int                   `json:"schema"`
	OK     bool                  `json:"ok"`
	Update autoupdate.UpdateView `json:"update"`
}

// runCheck 只查詢已簽章索引並記錄結果，不下載、不準備、不切換。查詢失敗是結果（check_failed），不是命令失敗。
func runCheck(base string, p autoupdate.Platform, timeout time.Duration, asJSON bool, w io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	s, err := (&autoupdate.Coordinator{Base: base, Platform: p}).Check(ctx)
	if err != nil {
		if asJSON {
			return failJSON(w, err)
		}
		return err
	}
	installed, _ := p.Installed()
	view := autoupdate.ViewOf(s, installed)
	if asJSON {
		return emitJSON(w, checkResult{Schema: contractSchema, OK: true, Update: view})
	}
	_, err = fmt.Fprintln(w, describeUpdate(view))
	return err
}
