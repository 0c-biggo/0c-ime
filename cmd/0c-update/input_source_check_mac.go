//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// 更新交接後的輸入來源自我檢查（#351）：只讀文字與檔案的部分放這裡，Linux 上也跑得到單元測試；查詢與操作系統的部分在
// input_source_check_darwin.go。
//
// 客戶在 macOS 27 回報背景更新後靈犀注音在輸入法選單變灰、要重開機（#340）。.15（26.7.3）照背景更新流程跑三次都正常，
// 但外部有同類案例：Meltype #134、#141（27.0.1）換掉 bundle 後系統的登記消失，重新 TISRegisterInputSource 就救回；
// killall TextInputMenuAgent 只修好選單的顯示；唯音在啟用前先重啟 TextInputMenuAgent；Apple DTS（forums 775526）說
// 輸入來源的設定登出前不一定重讀。所以每次安裝後、舊的輸入法程式交接完，由排程的 0c-update 在背景檢查一次：
//  1. 以 App 內附的 0c --inspect-input-source 查（新的行程，不受本行程的 TIS 快取影響）：父項與模式都登記著、
//     已啟用（在系統的啟用清單裡，#348），模式可以選取（選單裡不是灰的）就算正常。
//  2. 不正常就重新登記並啟用（0c --activate-input-source，與安裝時相同；已核准的來源不會再跳詢問），等一下再查。
//  3. 還不正常就重啟 TextInputMenuAgent（launchd 會自己帶起來），再查。
//  4. 仍不正常時記下來，交給登入後的補做（deferInputSource，#195）。
// 每一步的結果寫在 update/input-source-check.json，匯出狀態摘要時附上（shell.input_source_check）。

// inputSourceState 是 0c --inspect-input-source 對一個來源的回報。Enabled 是 TIS 顯示已啟用、而且在系統的啟用清單
// （com.apple.inputsources 的 AppleEnabledThirdPartyInputSources）裡；Listed 是單看清單的結果，讀不到清單時為 nil（#348）。
// 0.2.15 起模式預設啟用，TIS 一直把它顯示成已啟用，不在清單裡的模式重新登入後就不在選單列，所以只看 TIS 不夠。
type inputSourceState struct {
	ID            string `json:"id"`
	Present       bool   `json:"present"`
	Enabled       bool   `json:"enabled"`
	Listed        *bool  `json:"listed,omitempty"`
	SelectCapable bool   `json:"select_capable"`
	Selected      bool   `json:"selected"`
}

// inputSourceHealth：sources 依啟用順序，父項在前、可選取的來源在最後。
type inputSourceHealth struct {
	Sources []inputSourceState `json:"sources"`
}

func parseInputSourceHealth(out []byte) (inputSourceHealth, error) {
	var h inputSourceHealth
	if err := json.Unmarshal(out, &h); err != nil {
		return h, fmt.Errorf("看不懂輸入來源的查詢結果：%w", err)
	}
	if len(h.Sources) == 0 {
		return h, fmt.Errorf("輸入來源的查詢結果是空的")
	}
	return h, nil
}

// problem 回報哪裡不正常；正常時為空字串。每個來源都要登記著、已啟用，最後一個（可選取的模式）還要能選取。
func (h inputSourceHealth) problem() string {
	if len(h.Sources) == 0 {
		return "no input source"
	}
	var bad []string
	for i, s := range h.Sources {
		switch {
		case !s.Present:
			bad = append(bad, s.ID+" missing")
		case !s.Enabled && s.Listed != nil && !*s.Listed:
			bad = append(bad, s.ID+" not-listed")
		case !s.Enabled:
			bad = append(bad, s.ID+" disabled")
		case i == len(h.Sources)-1 && !s.SelectCapable:
			bad = append(bad, s.ID+" not-selectable")
		}
	}
	return strings.Join(bad, ", ")
}

// inputSourceCheckStep 是檢查的一步：query（查詢）、register（重新登記並啟用）、restart_menu_agent（重啟 TextInputMenuAgent）。
type inputSourceCheckStep struct {
	Step   string `json:"step"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// inputSourceCheck 是一次安裝之後的檢查結果，存在 update/input-source-check.json。Key 是版本與 App 執行檔的 inode：
// 同一次安裝只檢查一次，下一次安裝（或回復）換了 App 才再查。
type inputSourceCheck struct {
	Key     string                 `json:"key"`
	Version string                 `json:"version"`
	At      time.Time              `json:"at"`
	Outcome string                 `json:"outcome"` // ok、repaired_register、repaired_menu_agent、failed、query_failed
	Steps   []inputSourceCheckStep `json:"steps"`
}

// inputSourceRepair 是檢查用到的系統操作，測試時替換。
type inputSourceRepair struct {
	inspect     func() (inputSourceHealth, error)
	register    func() error
	restartMenu func() error
	wait        func(time.Duration)
}

// inputSourceSettle：重新登記、重啟 TextInputMenuAgent 之後等多久再查。換掉 App 後系統要幾秒才重新掃描完（InputSourceApproval.settle）。
const inputSourceSettle = 3 * time.Second

// run 依序查詢、重新登記、重啟 TextInputMenuAgent，回傳結果與每一步。第一次就查不到（工具失敗、逾時）時不修：
// 不知道狀態就不動使用者的輸入來源。
func (r inputSourceRepair) run() (string, []inputSourceCheckStep) {
	var steps []inputSourceCheckStep
	query := func() (bool, error) {
		h, err := r.inspect()
		if err != nil {
			steps = append(steps, inputSourceCheckStep{Step: "query", Detail: err.Error()})
			return false, err
		}
		bad := h.problem()
		steps = append(steps, inputSourceCheckStep{Step: "query", OK: bad == "", Detail: bad})
		return bad == "", nil
	}
	ok, err := query()
	if err != nil {
		return "query_failed", steps
	}
	if ok {
		return "ok", steps
	}
	if err := r.register(); err != nil {
		steps = append(steps, inputSourceCheckStep{Step: "register", Detail: err.Error()})
	} else {
		steps = append(steps, inputSourceCheckStep{Step: "register", OK: true})
	}
	r.wait(inputSourceSettle)
	if ok, _ := query(); ok {
		return "repaired_register", steps
	}
	if err := r.restartMenu(); err != nil {
		steps = append(steps, inputSourceCheckStep{Step: "restart_menu_agent", Detail: err.Error()})
	} else {
		steps = append(steps, inputSourceCheckStep{Step: "restart_menu_agent", OK: true})
	}
	r.wait(inputSourceSettle)
	if ok, _ := query(); ok {
		return "repaired_menu_agent", steps
	}
	return "failed", steps
}

// inspectSince：App 內附的 0c 從這一版起有 --inspect-input-source。更早的 0c 不認得的參數會當成輸入法程式啟動，不能呼叫。
const inspectSince = "0.2.15-0"

func inspectInputSourceSupported(v string) bool {
	order, err := update.CompareVersion(v, inspectSince)
	return err == nil && order >= 0
}

func inputSourceCheckFile(base string) string {
	return filepath.Join(base, "update", "input-source-check.json")
}

func loadInputSourceCheck(base string) *inputSourceCheck {
	b, err := os.ReadFile(inputSourceCheckFile(base))
	if err != nil {
		return nil
	}
	var c inputSourceCheck
	if json.Unmarshal(b, &c) != nil || c.Key == "" {
		return nil
	}
	return &c
}

func saveInputSourceCheck(base string, c inputSourceCheck) {
	if b, err := json.Marshal(c); err == nil {
		_ = os.MkdirAll(filepath.Dir(inputSourceCheckFile(base)), 0o700)
		_ = os.WriteFile(inputSourceCheckFile(base), append(b, '\n'), 0o600)
	}
}

// inputSourceCheckGate 是決定這次喚起能不能檢查的條件；wait 回傳還不能檢查的原因，可以檢查時為空字串。依序：已檢查過、App 太舊沒有查詢參數、
// 安裝時就沒啟用成功（由登入後的補做處理，#195）、要重新登入（簽章改變等著按「允許」）、剛裝好系統還在重新掃描、
// 舊的輸入法程式還沒交接、安全輸入開著（系統這時把第三方輸入法變灰，查到的不是更新造成的）。
type inputSourceCheckGate struct {
	Key          string
	Last         *inputSourceCheck
	Version      string
	Pending      bool
	Relogin      bool
	SinceInstall time.Duration
	ShellsLeft   bool
	SecureInput  bool
}

// inputSourceCheckMinAge：安裝後至少隔這麼久才查，避開系統重新掃描 App 的那幾秒。
const inputSourceCheckMinAge = 30 * time.Second

func (g inputSourceCheckGate) wait() string {
	switch {
	case g.Last != nil && g.Last.Key == g.Key:
		return "checked"
	case !inspectInputSourceSupported(g.Version):
		return "unsupported"
	case g.Pending:
		return "activation_pending"
	case g.Relogin:
		return "relogin_required"
	case g.SinceInstall < inputSourceCheckMinAge:
		return "settling"
	case g.ShellsLeft:
		return "handover_pending"
	case g.SecureInput:
		return "secure_input"
	}
	return ""
}
