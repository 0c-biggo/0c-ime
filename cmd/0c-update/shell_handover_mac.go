//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// macOS 的輸入法程式交接（#170）只讀文字與檔案的部分，放在這裡讓 Linux 上也跑得到單元測試；查詢與操作系統的
// 部分在 shell_handover_darwin.go。
//
// 0c.app 是 IMK 輸入法程式，系統在第一次用到時啟動、常駐到登出；安裝只換掉磁碟上的 App，執行中的那一份不會換新，
// 外殼的新功能要重新登入才生效。安裝、回復、解除安裝與排程喚起時，請本帳號執行中、不是目前安裝的那一份
// 0c.app（含 0.1.0-dev.21 以前的 Zuin.app）在使用者沒有組字、停手之後結束，再以不搶前景的方式（open -g；不加 -j，
// 隱藏啟動的 App 候選窗不會上螢幕，#194）啟動新的 App。做法同鼠鬚管安裝時結束舊的 Squirrel，但鼠鬚管與威注音都只在使用者自己執行安裝程式時做。
//
// 結束舊程式時，已經連著它的 App 不會自己換到新版：要等到焦點改變（切換 App、鎖定後解鎖、螢幕關閉後喚醒、
// 螢幕保護程式結束）才連上新的；在那之前按的第一個鍵會以英文字母直接交給 App，沒有先啟動新版時整批按鍵都會
// 漏成英文或被吃掉（#178，macOS 14／15／26 實測）。所以：
//   - 使用者自己安裝、回復（終端機、.pkg、設定程式「立即安裝」）時，使用者正看著安裝程式，之後回到原本的 App
//     就是一次焦點改變：停手後立即交接並啟動新版。
//   - 背景（排程、背景自動安裝）只在使用者離開時交接：這個帳號的螢幕已鎖定、螢幕保護程式或螢幕關閉中，或已切換
//     到別的使用者（macConsoleAway）。回來時的解鎖、喚醒就是焦點改變，一個字都不會漏。使用中停手多久都不交接，
//     舊程式搭新版引擎照常能打字，等下一次離開或重新登入。排程每 15 分鐘才喚起一次，0.2.4 起的輸入法程式在螢幕鎖定、
//     螢幕關閉或切換使用者時，若自己的執行檔已被換掉，就請 launchd 立即執行一次排程（Handover.swift，#195）。

// macShellHelper：同一個執行檔帶參數執行的是一次性工具（--activate-input-source、--update-agent、--update-notice
// 等），不是輸入法程式本身，不能結束。
func macShellHelper(args string) bool { return strings.Contains(args, " --") }

// underAny：path 在 roots 任一個目錄之下。
func underAny(path string, roots []string) bool {
	for _, root := range roots {
		if root != "" && strings.HasPrefix(filepath.Clean(path), filepath.Clean(root)+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// retirableMacShells 列出要結束的輸入法程式：App 在這個帳號自己的安裝位置（roots：Input Methods 與備份目錄）、
// 不是一次性工具（讀不到命令列的也不碰）、也不是目前安裝的那一份。current 是目前安裝的執行檔（解除安裝時為空，全部都要結束）；
// 同一路徑的程序以 isCurrent 核對是不是換新後的執行檔（inode），無法判斷時不碰。
func retirableMacShells(procs []macShellProcess, args map[string]string, roots []string, current string, isCurrent func(pid string) (bool, error)) []uiProcess {
	var out []uiProcess
	for _, p := range procs {
		cmdline, known := args[p.PID]
		if !known || macShellHelper(cmdline) || !underAny(p.Exe, roots) {
			continue
		}
		if current != "" && p.Exe == current {
			ok, err := isCurrent(p.PID)
			if err != nil || ok {
				continue
			}
		}
		pid, err := parsePID(p.PID)
		if err != nil {
			continue
		}
		out = append(out, uiProcess{PID: pid, Path: p.Exe})
	}
	return out
}

// awayCheckEvery：awayOnly 多久重新問一次使用者在不在（每次都要跑 ioreg）。retireProcesses 每 200 毫秒一輪、
// 每個程序各問一次停手多久，不必每次都重跑；晚一秒發現使用者回來也不會誤關，停手時間（idle）仍是每次即時讀的。
const awayCheckEvery = time.Second

// awayOnly 讓交接只在使用者離開時進行：人在的時候一律當成剛按過鍵（停手 0 秒），不會送出結束要求。
// 持續重新判斷（最多每 awayCheckEvery 一次）：等待中途使用者解鎖回來就停下。
func awayOnly(c uiControl, away func() bool) uiControl {
	idle, now := c.idle, c.now
	if now == nil {
		now = time.Now
	}
	var checked time.Time
	last := false
	c.idle = func() time.Duration {
		if t := now(); checked.IsZero() || t.Sub(checked) >= awayCheckEvery || t.Before(checked) {
			last, checked = away(), t
		}
		if !last {
			return 0
		}
		return idle()
	}
	return c
}

// handOverShells 請過期的輸入法程式結束；全部結束後呼叫 relaunch 啟動新版，縮短系統自己帶起新版之前的空窗。
// 還有舊程式沒結束時不啟動：同一個 bundle ID 的兩份 App 會搶同一個 IMK 連線名稱。
func handOverShells(c uiControl, pending func() []uiProcess, wait time.Duration, relaunch func()) (retired int, left []uiProcess) {
	retired, left = retireProcesses(c, pending, wait)
	if retired > 0 && len(left) == 0 && relaunch != nil {
		relaunch()
	}
	return retired, left
}

// macConsoleSessions 解析 `ioreg -n Root -d1` 輸出裡的 IOConsoleUsers：每個登入工作階段一個 key → value（去掉引號）。
// 值裡可能有逗號、大括號（例如全名），引號內的字元一律照抄；巢狀的值整段保留、不再展開。
func macConsoleSessions(ioreg string) []map[string]string {
	_, rest, ok := strings.Cut(ioreg, `"IOConsoleUsers" = (`)
	if !ok {
		return nil
	}
	var (
		out     []map[string]string
		cur     map[string]string
		tok     strings.Builder
		key     string
		haveKey bool
		depth   int // 0：陣列裡、字典之間；1：字典的 key=value 層；更深：巢狀的值
		quoted  bool
	)
	pair := func() {
		if haveKey && cur != nil {
			cur[key] = strings.Trim(strings.TrimSpace(tok.String()), `"`)
		}
		tok.Reset()
		key, haveKey = "", false
	}
	for _, r := range rest {
		if quoted {
			if r == '"' {
				quoted = false
			}
			tok.WriteRune(r)
			continue
		}
		switch {
		case r == '"':
			quoted = true
			tok.WriteRune(r)
		case r == '{' || r == '(':
			depth++
			if depth == 1 {
				cur, key, haveKey = map[string]string{}, "", false
				tok.Reset()
			} else {
				tok.WriteRune(r)
			}
		case (r == '}' || r == ')') && depth == 0:
			return out // 陣列結束
		case r == '}' || r == ')':
			if depth == 1 {
				pair()
				out = append(out, cur)
				cur = nil
			} else {
				tok.WriteRune(r)
			}
			depth--
		case r == '=' && depth == 1 && !haveKey:
			key, haveKey = strings.Trim(strings.TrimSpace(tok.String()), `"`), true
			tok.Reset()
		case r == ',' && depth == 1:
			pair()
		case r == '\n' && depth == 0:
			return out
		case depth >= 1:
			tok.WriteRune(r)
		}
	}
	return nil // 沒有正常結束：格式看不懂
}

// macConsoleAway：uid 這個帳號的使用者是不是離開了電腦——自己的工作階段標示螢幕已鎖定（CGSSessionScreenIsLocked：
// 鎖定畫面、螢幕保護程式、螢幕關閉時都會設，macOS 15 實測），或不在主控台（快速切換到別的使用者）。
// 找不到自己的工作階段、或輸出看不懂時回 false：無法確認就當成人還在，不交接。
func macConsoleAway(ioreg string, uid int) bool {
	for _, s := range macConsoleSessions(ioreg) {
		if s["kCGSSessionUserIDKey"] != strconv.Itoa(uid) {
			continue
		}
		return s["CGSSessionScreenIsLocked"] == "Yes" || s["kCGSSessionOnConsoleKey"] == "No"
	}
	return false
}

// deferredInputSource 解析 deferInputSource 記下的輸入來源：第一行是登記用的工具（App 內的 0c），第二行是 App。
// 記錄看不懂、或 App 已經不在時 ok 為 false，呼叫端刪掉記錄，不再每次喚起都重試（#195）。記下的工具已不在
// （回復時用的是移入備份的新版工具，備份之後被清掉）時改用 App 本身的 0c；也沒有時一樣放棄。
func deferredInputSource(b []byte, exists func(string) bool) (tool, app string, ok bool) {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		return "", "", false
	}
	tool, app = strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
	if app == "" || !exists(app) {
		return "", "", false
	}
	if tool == "" || !exists(tool) {
		tool = filepath.Join(app, "Contents/MacOS/0c")
		if !exists(tool) {
			return "", "", false
		}
	}
	return tool, app, true
}

// loginStamp 記下某個時間點所在的登入工作階段（#195）：ASID 取自 launchctl 的 gui 網域，取不到時為 0；
// Headless 表示當時確定這個帳號沒有圖形登入（IOConsoleUsers 裡沒有它的工作階段，例如只從 SSH 或在登入畫面安裝）。
// 舊紀錄沒有 Headless，與「當時有圖形登入但取不到 ASID」同樣保守處理（見 crossed）。
type loginStamp struct {
	ASID     uint64    `json:"asid,omitempty"`
	At       time.Time `json:"at"`
	Headless bool      `json:"headless,omitempty"`
}

// crossed：這個紀錄之後（現在的工作階段 current、開機時間 boot）是否已跨過登入邊界：之後重新開機過，或現在取得到
// ASID 且與記下的不同。記下的是 0 時：當時確定沒有圖形登入，現在取得到 ASID 就是之後才登入的，算跨過（不算的話
// 要等重新開機，比 0.2.3 以前每次登入都補做還差）；當時有圖形登入只是取不到 ASID、或舊紀錄不知道時，不能確定
// 現在是不是同一個工作階段，不算跨過，回 adopt 讓呼叫端把現在的 ASID 記下來，之後 ASID 改變才算。
// 現在取不到 ASID 時無法判斷，不算。
func (s loginStamp) crossed(current uint64, boot time.Time) (crossed, adopt bool) {
	switch {
	case !boot.IsZero() && boot.After(s.At):
		return true, false
	case current == 0:
		return false, false
	case s.ASID != 0:
		return s.ASID != current, false
	case s.Headless:
		return true, false
	}
	return false, true
}

// macGUILogin 從 `ioreg -n Root -d1` 的 IOConsoleUsers 判斷 uid 這個帳號有沒有圖形登入工作階段（含快速切換到別的
// 使用者後留在背景的）。known 為 false 表示輸出看不懂，呼叫端不能當成沒有登入。
func macGUILogin(ioreg string, uid int) (has, known bool) {
	if !strings.Contains(ioreg, `"IOConsoleUsers" = (`) {
		return false, strings.Contains(ioreg, "+-o Root") // 沒有人登入過時沒有這個屬性
	}
	if strings.Contains(ioreg, `"IOConsoleUsers" = ()`) {
		return false, true
	}
	sessions := macConsoleSessions(ioreg)
	if sessions == nil {
		return false, false
	}
	for _, s := range sessions {
		if s["kCGSSessionUserIDKey"] == strconv.Itoa(uid) {
			return true, true
		}
	}
	return false, true
}

// relogin-required 記號的內容：第一行是安裝時的 ASID，確定沒有圖形登入時第二行是 headless。
func formatReloginMarker(s loginStamp) []byte {
	b := strconv.FormatUint(s.ASID, 10) + "\n"
	if s.ASID == 0 && s.Headless {
		b += "headless\n"
	}
	return []byte(b)
}

func parseReloginMarker(b []byte, at time.Time) loginStamp {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	asid, _ := strconv.ParseUint(strings.TrimSpace(lines[0]), 10, 64)
	return loginStamp{ASID: asid, At: at, Headless: len(lines) > 1 && strings.TrimSpace(lines[1]) == "headless"}
}

// 安裝後要重新登入的情況：輸入來源還沒登記好（deferInputSource 記下、登入後補做），或簽章身分改變、要等使用者
// 按「允許」。這時舊的輸入法程式照常能打字，不結束它；新版在重新登入後載入。
func reloginMarker(base string) string { return filepath.Join(base, "update", "relogin-required") }

// reloginRequired：還在安裝時的同一個登入工作階段、且安裝時記下要重新登入。跨過登入邊界（loginStamp.crossed）後
// 清掉記號；安裝時有圖形登入但沒記到 ASID 的，第一次取得 ASID 時記下來，仍算同一個工作階段。
// 輸入來源還沒啟用的記號（input-source）不算：記下它的安裝都同時寫了 relogin-required，同一個工作階段由後者判斷；
// 跨過登入邊界後由排程補做啟用，補不成功也不該讓之後每次更新都要求重新登入（#195：dev.61 留下的記號讓之後每一版都卡在
// 「重新登入後生效」）。
func reloginRequired(base string, current uint64, boot time.Time) bool {
	path := reloginMarker(base)
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	crossed, adopt := parseReloginMarker(b, info.ModTime()).crossed(current, boot)
	if crossed {
		os.Remove(path)
		return false
	}
	if adopt {
		_ = os.WriteFile(path, formatReloginMarker(loginStamp{ASID: current}), 0o600)
	}
	return true
}

// inputSourceRetry 是排程補做輸入來源啟用的紀錄（update/input-source-retry.json，與 input-source 記號並存）。
// 啟用失敗時（使用者在系統詢問按「不允許」或沒理它、新的輸入模式要重新登入才登記得到）重試只會每次再跳一次詢問
// （.12 實測：按了「不允許」，下一次執行 --activate-input-source 又跳），所以同一個登入工作階段只試一次：安裝、
// 回復當下的那次算這個工作階段的；跨過登入邊界（同 relogin-required 的判斷）再試，跟 0.2.3 以前登入時補做一樣。
// 沒有紀錄的記號（0.2.3 以前留下的，#195 回報者的情形）立即試一次。排程補做連續在 inputSourceRetrySessions 個
// 登入工作階段失敗就停止，留下記號與最後的錯誤給狀態摘要；之後的安裝啟用成功時一併清掉。
type inputSourceRetry struct {
	ASID     uint64    `json:"asid,omitempty"` // 最後一次嘗試所在的登入工作階段（loginStamp）；取不到時為 0
	At       time.Time `json:"at"`
	Headless bool      `json:"headless,omitempty"` // 最後一次嘗試時確定沒有圖形登入
	Failed   int       `json:"failed,omitempty"`   // 排程補做失敗過的登入工作階段數（不含安裝當下那次）
	Error    string    `json:"error,omitempty"`
	// Queried：不補做時最後一次查詢（--query-input-source）所在的登入工作階段；一個工作階段只查一次。
	Queried *loginStamp `json:"queried,omitempty"`
}

const inputSourceRetrySessions = 3

func (r *inputSourceRetry) session() loginStamp {
	return loginStamp{ASID: r.ASID, At: r.At, Headless: r.Headless}
}

// queryDue：這個登入工作階段還沒查詢過。上次查詢時有圖形登入但沒記到 ASID 的，與補做一樣把這次的 ASID 記成那次的。
func (r *inputSourceRetry) queryDue(base string, cur loginStamp, boot time.Time) bool {
	if r.Queried == nil {
		return true
	}
	crossed, adopt := r.Queried.crossed(cur.ASID, boot)
	if adopt {
		r.Queried.ASID, r.Queried.Headless = cur.ASID, false
		saveInputSourceRetry(base, *r)
	}
	return crossed
}

func (r *inputSourceRetry) stopped() bool { return r != nil && r.Failed >= inputSourceRetrySessions }

// failed 回傳這次（工作階段 cur）補做失敗後的紀錄。
func (r *inputSourceRetry) failed(cur loginStamp, err error) inputSourceRetry {
	next := inputSourceRetry{ASID: cur.ASID, At: cur.At, Headless: cur.Headless, Failed: 1, Error: err.Error()}
	if r != nil {
		next.Failed = r.Failed + 1
	}
	return next
}

func parseInputSourceRetry(b []byte) *inputSourceRetry {
	var r inputSourceRetry
	if json.Unmarshal(b, &r) != nil || r.At.IsZero() {
		return nil
	}
	return &r
}

func inputSourceMarker(base string) string { return filepath.Join(base, "update", "input-source") }

func inputSourceRetryFile(base string) string {
	return filepath.Join(base, "update", "input-source-retry.json")
}

// clearInputSource：輸入來源已啟用（或 App 已不在），刪掉記號與補做紀錄。
func clearInputSource(base string) {
	os.Remove(inputSourceMarker(base))
	os.Remove(inputSourceRetryFile(base))
}

func loadInputSourceRetry(base string) *inputSourceRetry {
	b, err := os.ReadFile(inputSourceRetryFile(base))
	if err != nil {
		return nil
	}
	return parseInputSourceRetry(b)
}

func saveInputSourceRetry(base string, r inputSourceRetry) {
	if b, err := json.Marshal(r); err == nil {
		_ = os.WriteFile(inputSourceRetryFile(base), append(b, '\n'), 0o600)
	}
}

// retryInputSource 啟用安裝或回復時記下的輸入來源，同一個登入工作階段只試一次（inputSourceRetry）：成功後清除記錄，
// 失敗則記下，跨過登入邊界再試；連續 inputSourceRetrySessions 個登入工作階段都失敗就停止，記進更新紀錄。
// 不補做的喚起（這個工作階段已試過、或已停止補做）只在 query 時（背景排程的 auto；設定程式「立即安裝」、選單、
// 通知按鈕等著結果的呼叫不查）以 enabled 查詢、不啟用，而且同一個登入工作階段只查一次（Queried）：使用者自己到
// 系統設定加入，或之後才在系統詢問按「允許」時，最晚下一次登入記號與紀錄就清掉，狀態摘要不再顯示還沒啟用。
// 記錄看不懂、App 已不在時直接清掉。cur 是目前的登入工作階段（At 為現在），boot 是開機時間。
func retryInputSource(base string, cur loginStamp, boot time.Time, query bool, activate func(tool, app string, force ...bool) error, enabled func(tool, app string) bool) error {
	b, err := os.ReadFile(inputSourceMarker(base))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	tool, app, ok := deferredInputSource(b, func(p string) bool { _, err := os.Stat(p); return err == nil })
	if !ok {
		clearInputSource(base)
		return nil
	}
	r := loadInputSourceRetry(base)
	if r != nil {
		crossed, adopt := r.session().crossed(cur.ASID, boot)
		if adopt { // 嘗試時有圖形登入但沒記到 ASID：現在這個就是那個工作階段
			r.ASID, r.Headless = cur.ASID, false
			saveInputSourceRetry(base, *r)
		}
		if r.stopped() || !crossed {
			if query && r.queryDue(base, cur, boot) {
				r.Queried = &cur
				saveInputSourceRetry(base, *r)
				if enabled(tool, app) {
					clearInputSource(base)
				}
			}
			return nil
		}
	}
	if err = activate(tool, app); err == nil {
		clearInputSource(base)
		return nil
	}
	next := r.failed(cur, err)
	saveInputSourceRetry(base, next)
	if next.stopped() {
		_, _ = autoupdate.EditState(base, func(s *autoupdate.State) error {
			s.Record("input_source_retry_stopped", next.Error)
			return nil
		})
		return fmt.Errorf("已在 %d 次登入後補做仍未啟用，不再自動重試；請在「系統設定 → 鍵盤 → 文字輸入 → 編輯」加入「靈犀注音」並允許（%w）", next.Failed, err)
	}
	return err
}

// confirmedInputSource 解析 `0c --query-input-source <App>`（輸出與 --confirm-input-source 相同，但不改選）：每行
// 「<ID> enabled|disabled|missing」，依啟用順序列出、可選取的來源在最後；--confirm-input-source 在可選取的來源已啟用、
// 改選了它時再多一行「<ID> selected」。回報可選取的來源是否已啟用，
// 與安裝時 --activate-input-source 判斷成功的條件相同（InputSource.swift 的 confirm）。
func confirmedInputSource(out string) bool {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	fields := strings.Fields(lines[len(lines)-1])
	return len(fields) == 2 && (fields[1] == "enabled" || fields[1] == "selected")
}

// querySince：App 內附的 0c 從這一版起有只查詢、絕不改選的 --query-input-source（#195）。更早的只有
// --confirm-input-source，來源已啟用而目前選著靈犀注音的其他來源時會改選，背景排程不能用它；更早的 0c 不認得的參數
// 還會當成輸入法程式啟動。所以先看版本，太舊的不查。
const querySince = "0.2.4"

// queryInputSourceSupported：版本 v 的 App 內附的 0c 有 --query-input-source（querySince 起）。
func queryInputSourceSupported(v string) bool {
	order, err := update.CompareVersion(v, querySince)
	return err == nil && order >= 0
}

func parsePID(s string) (uint32, error) {
	pid, err := strconv.ParseUint(s, 10, 32)
	return uint32(pid), err
}

// macNoShiftSince 是 macOS 中英切換鍵預設改成「不用 Shift」的第一個版本（#167）；更早的版本預設右 Shift。
const macNoShiftSince = "0.2.1"

// macKeepsRightShift：這次安裝是不是從右 Shift 還是預設的版本升級。prev 是升級前的收據（沒有收據時為 nil），
// appExisted 表示升級前已有 App（沒有收據的原型、或收據遺失）。全新安裝不寫，採 macOS 預設。
func macKeepsRightShift(prev *update.MacReceipt, appExisted bool) bool {
	if prev == nil {
		return appExisted
	}
	if prev.Version == "" || !update.ValidVersion(prev.Version) {
		return true
	}
	order, err := update.CompareVersion(prev.Version, macNoShiftSince)
	return err == nil && order < 0
}

// macUpgradedFromRightShift 在安裝完成之後判斷（背景更新由舊版更新器安裝時，新版的程式碼要事後補做）：
// 備份裡的上一個安裝是 0.2.1 以前的版本。不看目前的版本：0.2.1 的候選版（0.2.1-rc 等）也已改成「不用 Shift」；
// 回復到舊版時，上一個安裝是較新的版本，不會寫入。
func macUpgradedFromRightShift(support string) bool {
	current, err := readMacReceiptFile(filepath.Join(support, "install-receipt.json"))
	if err != nil || current == nil || !current.Active || current.Previous == "" || !update.SafeName(current.Previous) {
		return false
	}
	dir := filepath.Join(support, filepath.FromSlash(current.Previous))
	prev, err := readMacReceiptFile(filepath.Join(dir, "previous.json"))
	if err != nil {
		return false // 備份已被清掉或讀不到：無法判斷就不改設定
	}
	existed := false
	for _, name := range []string{update.MacApp, update.LegacyMacApp} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			existed = true
		}
	}
	return macKeepsRightShift(prev, existed)
}

// readMacReceiptFile 讀收據；檔案內容是 null（全新安裝的備份）時回傳 nil。
func readMacReceiptFile(path string) (*update.MacReceipt, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r *update.MacReceipt
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return r, nil
}
