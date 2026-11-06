package main

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 狀態摘要的「最近用過的程式」（#257，Windows）：從 TSF 外殼的 tsf.log 整理每個程式最近一次啟用輸入法的時間，
// 以及輸入法在那個程式裡的判定（欄位政策、文件種類、候選窗位置的退路、提交失敗等）。客服拿到使用者自己匯出的
// 狀態檔，就知道 AutoCAD 這類程式裡發生什麼，不必請使用者找紀錄檔。
//
// tsf.log 本來就只記事件不記文字（platforms/windows/tsf/src/log.rs）；這裡再保守一層：
//   - tsf-debug 開著時的鍵碼除錯紀錄（"debug " 開頭，或任何帶 vk= 的行）整行不讀；
//   - 程式只留檔名，不留路徑；細節裡的 Windows 路徑一律換成檔名（避免帶出使用者名稱）；
//   - 引擎回傳的錯誤訊息只計次，不抄內容；
//   - 只讀檔案尾端 tsfLogTail 位元組、只算最近 tsfAppsWindow 內、最多 tsfAppsLimit 個程式。
const (
	tsfLogTail      = 4 << 20 // 外殼超過 4 MiB 就重來，等於整個檔
	tsfAppsWindow   = 7 * 24 * time.Hour
	tsfAppsLimit    = 30
	tsfDetailsLimit = 8
	tsfDetailMax    = 240
)

type tsfApp struct {
	Exe      string         `json:"exe"`
	LastSeen time.Time      `json:"last_seen"`
	X86      bool           `json:"x86,omitempty"`
	Flags    []string       `json:"activate_flags,omitempty"`
	Events   map[string]int `json:"events"`
	Details  []string       `json:"details,omitempty"`

	starts, dones int
}

type tsfLogSummary struct {
	Bytes   int64      `json:"bytes"`
	Partial bool       `json:"partial,omitempty"` // 檔案比 tsfLogTail 大，只讀了尾端
	Since   *time.Time `json:"since,omitempty"`   // 統計涵蓋的第一筆紀錄
	Apps    []*tsfApp  `json:"apps"`
}

// tsfAppsFromFile 讀 tsf.log 的尾端並整理；檔案不存在時回 nil（沒有裝過 TSF 外殼，或外殼寫不了檔）。
func tsfAppsFromFile(path string, now time.Time) *tsfLogSummary {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	partial := info.Size() > tsfLogTail
	if partial {
		if _, err := f.Seek(-tsfLogTail, io.SeekEnd); err != nil {
			return nil
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, tsfLogTail))
	if err != nil {
		return nil
	}
	if partial {
		// 從中間開始讀時第一行不完整。
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	s := summarizeTSFLog(bytes.NewReader(data), now)
	s.Bytes, s.Partial = info.Size(), partial
	return s
}

var (
	tsfLinePattern  = regexp.MustCompile(`^(\d{10,16}) pid=(\d+) (.*)$`)
	tsfExePattern   = regexp.MustCompile(`exe=(.+?\.[eE][xX][eE])(?:\s|$)`)
	tsfExeFallback  = regexp.MustCompile(`exe=(\S+)`)
	tsfFlagsPattern = regexp.MustCompile(`^activate flags=(0x[0-9a-fA-F]+)`)
	tsfPathPattern  = regexp.MustCompile(`(?i)(?:[a-z]:|\\\\[^\\\s]+)\\[^\s]*`)
	// WPF 的視窗類別帶每個行程不同的 GUID（HwndWrapper[程式;;GUID]）：換掉才能跨行程去重。
	tsfGUIDPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
)

// summarizeTSFLog 把 tsf.log 的內容整理成每個程式一筆，依最近一次出現排序。
// 每行是「毫秒時間 pid=行程 訊息」；只有部分訊息帶 exe=，其他行以同一個行程最近一次帶 exe= 的行歸到那個程式。
func summarizeTSFLog(r io.Reader, now time.Time) *tsfLogSummary {
	cutoff := now.Add(-tsfAppsWindow)
	apps := map[string]*tsfApp{}
	exeOf := map[string]string{}   // pid → 程式
	pending := map[string]string{} // pid → 下一次 activate 開始的啟用旗標（flags 那行在 exe 那行之前）
	var since time.Time
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		m := tsfLinePattern.FindStringSubmatch(strings.TrimRight(sc.Text(), "\r"))
		if m == nil {
			continue
		}
		msg := m[3]
		if strings.HasPrefix(msg, "debug ") || strings.Contains(msg, "vk=") {
			continue // tsf-debug 的鍵碼紀錄
		}
		ms, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			continue
		}
		at := time.UnixMilli(ms)
		pid := m[2]
		if f := tsfFlagsPattern.FindStringSubmatch(msg); f != nil {
			pending[pid] = strings.ToLower(f[1])
			continue
		}
		if exe := tsfExe(msg); exe != "" {
			exeOf[pid] = exe
		}
		exe := exeOf[pid]
		if exe == "" || at.Before(cutoff) {
			continue
		}
		kind, detail := classifyTSFLine(msg)
		if kind == "" {
			continue
		}
		key := strings.ToLower(exe)
		app := apps[key]
		if app == nil {
			app = &tsfApp{Exe: exe, Events: map[string]int{}}
			apps[key] = app
		}
		if at.After(app.LastSeen) {
			app.LastSeen = at
		}
		if since.IsZero() || at.Before(since) {
			since = at
		}
		switch kind {
		case "activate":
			app.starts++
			if strings.HasSuffix(msg, " x86") {
				app.X86 = true
			}
			if f, ok := pending[pid]; ok {
				delete(pending, pid)
				if !slices.Contains(app.Flags, f) && len(app.Flags) < tsfDetailsLimit {
					app.Flags = append(app.Flags, f)
				}
			}
		case "activate_done":
			app.dones++
			if strings.Contains(msg, "ui_less=true") {
				app.Events["ui_less"]++
			}
			continue // 只用來算有沒有啟用到一半
		}
		app.Events[kind]++
		if detail != "" && len(app.Details) < tsfDetailsLimit && !slices.Contains(app.Details, detail) {
			app.Details = append(app.Details, detail)
		}
	}
	list := make([]*tsfApp, 0, len(apps))
	for _, app := range apps {
		if n := app.starts - app.dones; n > 0 {
			app.Events["activate_unfinished"] = n // 啟用開始了沒有完成：宿主在啟用中途崩潰或卡住（#89）
		}
		app.LastSeen = app.LastSeen.Truncate(time.Second)
		list = append(list, app)
	}
	sort.Slice(list, func(i, j int) bool {
		if !list[i].LastSeen.Equal(list[j].LastSeen) {
			return list[i].LastSeen.After(list[j].LastSeen)
		}
		return list[i].Exe < list[j].Exe
	})
	if len(list) > tsfAppsLimit {
		list = list[:tsfAppsLimit]
	}
	out := &tsfLogSummary{Apps: list}
	if !since.IsZero() {
		s := since.Truncate(time.Second)
		out.Since = &s
	}
	return out
}

// tsfExe 取出訊息裡的程式檔名；外殼記的本來就是檔名，這裡再去掉任何路徑。
func tsfExe(msg string) string {
	m := tsfExePattern.FindStringSubmatch(msg)
	if m == nil {
		m = tsfExeFallback.FindStringSubmatch(msg)
	}
	if m == nil || m[1] == "?" {
		return ""
	}
	return baseName(m[1])
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// classifyTSFLine 回傳判定種類與（可以列出的）細節；不認得或不是判定的行回空字串。
// 細節只取外殼為診斷而寫的固定格式（欄位政策、文件種類、候選窗、提交失敗），不含輸入內容。
func classifyTSFLine(msg string) (kind, detail string) {
	has := func(prefix string) bool { return strings.HasPrefix(msg, prefix) }
	switch {
	case has("activate 開始"):
		return "activate", ""
	case has("activate 完成"):
		return "activate_done", ""
	case has("文件："):
		kind := "context_tsf"
		if strings.Contains(msg, "IMM32") {
			kind = "context_imm32"
		}
		return kind, tsfDetail(msg)
	case has("欄位政策："):
		switch {
		case strings.Contains(msg, "以私人方式組字"):
			kind = "field_private"
		case strings.Contains(msg, "密碼或 PIN"):
			kind = "field_password"
		case strings.Contains(msg, "程式停用了"):
			kind = "field_disabled"
		case strings.Contains(msg, "沒有焦點文件"):
			kind = "field_no_document"
		case strings.Contains(msg, "讀不到 InputScope"):
			kind = "field_no_inputscope_passthrough"
		case strings.Contains(msg, "沒有回報選取範圍"):
			kind = "field_no_selection"
		case strings.Contains(msg, "讀不到"):
			kind = "field_unreadable"
		default:
			kind = "field_other"
		}
		return kind, tsfDetail(msg)
	case has("英文模式：IMM32"):
		return "imm32_no_caret", tsfDetail(msg)
	case has("候選窗："):
		return "candidate_fallback", tsfDetail(msg)
	case has("ui: 宿主行程內候選窗（"):
		return "candidate_in_host", ""
	case has("ui: 宿主自行繪製候選"):
		return "candidate_host_drawn", ""
	case has("組字被宿主終止"):
		return "composition_terminated", ""
	case has("edit session"), has("RequestEditSession"), has("非同步"), has("提交"), has("沒有 context 可提交"), has("顯示屬性設定失敗"):
		return "edit_failed", tsfDetail(msg)
	case has("TestKeyDown 已吃下的鍵引擎沒有處理"):
		return "key_typed_literal", ""
	case has("handle: server 不可用"):
		return "engine_unavailable", ""
	case has("open: session="):
		return "", ""
	case has("open: "), has("handle: "), has("connect: 失敗"), has("call: "):
		return "engine_error", "" // 引擎的錯誤訊息不抄
	case has("spawn: "):
		if strings.Contains(msg, "失敗") || has("spawn: 沒有") {
			return "spawn_failed", "" // 訊息帶安裝路徑（含使用者名稱）
		}
	case has("ui: "), has("langbar: 連不上"), has("langbar: 前景權限交不出去"), has("langbar: 建立背景執行緒失敗"):
		if strings.Contains(msg, "失敗") || strings.Contains(msg, "連不上") || strings.Contains(msg, "交不出去") {
			return "ui_error", ""
		}
	case has("panic"):
		return "panic", tsfDetail(msg)
	}
	return "", ""
}

// tsfDetail 去掉訊息裡的程式名稱（已經在 exe 欄）與路徑，限制長度。
func tsfDetail(msg string) string {
	msg = tsfExePattern.ReplaceAllString(msg, "")
	msg = tsfPathPattern.ReplaceAllStringFunc(msg, baseName)
	msg = tsfGUIDPattern.ReplaceAllString(msg, "GUID")
	msg = strings.Join(strings.Fields(msg), " ")
	if r := []rune(msg); len(r) > tsfDetailMax {
		msg = string(r[:tsfDetailMax]) + "…"
	}
	return msg
}
