//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// 安裝時「允許」晚出現的補做（#348）：只讀文字與檔案、決定下一步的部分放這裡，Linux 上也跑得到單元測試；查詢、
// 啟用與背景程式在 input_source_followup_darwin.go。
//
// 第三方輸入來源要依序核准父項與模式（#238）：安裝時 0c --activate-input-source 送出父項的要求後最多等 60 秒。
// 系統設定很慢時（#339 的 Intel VM），「允許」在 60 秒後才出現，安裝已經放棄、記下登入後補做；使用者按了「允許」，
// 系統只啟用父項，模式從沒送出要求，選單裡沒有靈犀注音（父項不能選取），要等重新登入後排程再問一次。
//
// 要求送出後，送出的程式結束了，「允許」照樣生效（#238 實測），所以不必讓安裝程式一直等：安裝後由背景排程的
// auto 在同一個登入工作階段接手，父項一被允許就請系統啟用模式（再跳一次詢問），不必重新登入。
//   - 安裝後 inputSourceFollowUpWatch 內：另起一個脫離排程的程式（0c-update input-source-followup），每隔幾秒用
//     0c --inspect-input-source 查一次（新的行程，不受 TIS 快取影響，#351），使用者按下「允許」後幾秒內就接著詢問模式。
//   - 之後到 inputSourceFollowUpWindow 為止：每次排程喚起只查一次，條件成立就在這次喚起裡送出模式的要求。
//   - 父項從沒被允許（使用者按了「不允許」或沒理）時只查、不再送父項的要求，不會一直跳詢問；模式的要求每個登入
//     工作階段只送一次（input-source-followup.json），使用者拒絕了就留給登入後的補做，與 #195 的規則相同。
// 父項與模式都啟用後清掉 input-source 記號與補做紀錄，並與安裝當下啟用成功一樣不必再重新登入。
//
// 0.2.15 起模式預設啟用（Info.plist 的 tsInputModeDefaultStateKey，#348 後續）：父項晚一點被允許時，系統把父項與模式一起
// 寫進啟用清單，補做下一次查詢就是 followUpDone，不必再問。送模式要求的分支留給清單裡只有父項的情況（例如系統沒有一起
// 寫進模式）。「已啟用」以 0c --inspect-input-source 的 enabled 為準，它看系統的啟用清單，不是只看 TIS。

const (
	// inputSourceFollowUpWatch：安裝（或回復）時記下補做後，背景程式持續查詢的時間。
	inputSourceFollowUpWatch = 30 * time.Minute
	// inputSourceFollowUpWindow：之後每次排程喚起還查一次的時間；超過就只剩登入後的補做與每個工作階段一次的查詢（#195）。
	inputSourceFollowUpWindow = 24 * time.Hour
	// inputSourceFollowUpPoll：背景程式查詢的間隔。
	inputSourceFollowUpPoll = 2 * time.Second
)

type followUpAction int

const (
	followUpWait        followUpAction = iota // 父項還沒啟用（等使用者按「允許」）或查不到
	followUpRequestMode                       // 父項都已啟用、模式還沒：請系統啟用模式
	followUpDone                              // 可選取的來源已啟用
)

// followUpStep 依 0c --inspect-input-source 的結果決定下一步。sources 依啟用順序，最後一個是可選取的來源。
// 沒有模式的舊 App 只有一個來源：它的要求在安裝時已送出，只等，不重送。
func followUpStep(h inputSourceHealth) followUpAction {
	n := len(h.Sources)
	if n == 0 {
		return followUpWait
	}
	if last := h.Sources[n-1]; last.Present && last.Enabled {
		return followUpDone
	}
	if n == 1 {
		return followUpWait
	}
	for _, s := range h.Sources[:n-1] {
		if !s.Present || !s.Enabled {
			return followUpWait
		}
	}
	return followUpRequestMode
}

// inputSourceFollowUp 是補做用到的操作，測試時替換。
type inputSourceFollowUp struct {
	inspect func() (inputSourceHealth, error)
	// activate 請系統啟用模式：0c --activate-input-source 跳過已啟用的父項，只送出模式的要求並等「允許」。
	activate func() error
	// requested／markRequested：這個登入工作階段是不是已經送過模式的要求。
	requested     func() bool
	markRequested func()
	// finish：已啟用，清掉記號。
	finish func()
	now    func() time.Time
	wait   func(time.Duration)
	until  time.Time // 查到這個時間為止；不晚於現在時只查一次
	poll   time.Duration
}

// run 回傳結果：enabled（已啟用）、requested（這次送了模式的要求，還沒啟用）、waiting（父項還沒啟用、查不到，或模式的要求
// 之前已送過、還在等）。
func (f inputSourceFollowUp) run() string {
	outcome := "waiting"
	for {
		if h, err := f.inspect(); err == nil {
			switch followUpStep(h) {
			case followUpDone:
				f.finish()
				return "enabled"
			case followUpRequestMode:
				if !f.requested() {
					f.markRequested()
					if f.activate() == nil {
						f.finish()
						return "enabled"
					}
					// 沒等到「允許」：要求已送出，之後才按照樣生效，繼續查，不再送。
					outcome = "requested"
					continue
				}
			}
		}
		if !f.now().Before(f.until) {
			return outcome
		}
		f.wait(f.poll)
	}
}

// followUpPlan 是排程喚起時對補做的安排。
type followUpPlan int

const (
	followUpNone   followUpPlan = iota // 不在補做的時間內：照舊（每個工作階段查一次，跨過登入再補做）
	followUpSpawn                      // 起背景程式持續查詢
	followUpInline                     // 在這次喚起裡查一次
)

// planFollowUp：since 是安裝（或回復）時記下補做到現在的時間。
func planFollowUp(since time.Duration) followUpPlan {
	switch {
	case since < 0 || since >= inputSourceFollowUpWindow:
		return followUpNone
	case since < inputSourceFollowUpWatch:
		return followUpSpawn
	}
	return followUpInline
}

// inputSourceFollowUpRecord（update/input-source-followup.json）：這個登入工作階段已送過模式的要求。與補做紀錄
// （input-source-retry.json）分開存：背景程式與排程同時寫時不會蓋掉對方的欄位。
type inputSourceFollowUpRecord struct {
	ModeRequested *loginStamp `json:"mode_requested,omitempty"`
}

func inputSourceFollowUpFile(base string) string {
	return filepath.Join(base, "update", "input-source-followup.json")
}

// modeRequestedIn：記下的模式要求是不是在 cur 這個登入工作階段送的。跨過登入邊界（或判斷不了）就不算，交給登入後的補做。
func modeRequestedIn(base string, cur loginStamp, boot time.Time) bool {
	b, err := os.ReadFile(inputSourceFollowUpFile(base))
	if err != nil {
		return false
	}
	var r inputSourceFollowUpRecord
	if json.Unmarshal(b, &r) != nil || r.ModeRequested == nil {
		return false
	}
	crossed, _ := r.ModeRequested.crossed(cur.ASID, boot)
	return !crossed
}

func saveModeRequested(base string, cur loginStamp) {
	if b, err := json.Marshal(inputSourceFollowUpRecord{ModeRequested: &cur}); err == nil {
		_ = os.MkdirAll(filepath.Dir(inputSourceFollowUpFile(base)), 0o700)
		_ = os.WriteFile(inputSourceFollowUpFile(base), append(b, '\n'), 0o600)
	}
}

// finishInputSourceFollowUp：已啟用。與安裝當下啟用成功相同：清掉補做記號；不是簽章改變的安裝就不必再重新登入，
// 舊的輸入法程式由之後的排程在使用者離開時交接（handOverUI）。
func finishInputSourceFollowUp(base string, r *inputSourceRetry) {
	clearInputSource(base)
	if r == nil || !r.KeepRelogin {
		os.Remove(reloginMarker(base))
	}
}
