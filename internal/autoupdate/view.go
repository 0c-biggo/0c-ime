package autoupdate

import "time"

// 更新查詢的四種狀態。介面據此顯示，不把「無法檢查」或「還沒檢查」說成「已是最新」。
const (
	CheckNever           = "never_checked"    // 從來沒有查詢過
	CheckUpToDate        = "up_to_date"       // 最近一次成功查詢時，沒有可更新的版本
	CheckUpdateAvailable = "update_available" // 已知有可更新的版本（可能還在下載、準備或等待生效）
	CheckFailed          = "check_failed"     // 最近一次查詢失敗，且沒有已知的待更新版本
)

// UpdateView 是 State 中與更新查詢有關的欄位，整理成介面直接可用的形式；不含個人資料。
type UpdateView struct {
	State         string      `json:"state"`
	Installed     string      `json:"installed,omitempty"`
	LastCheck     *time.Time  `json:"last_check,omitempty"`
	LastSuccess   *time.Time  `json:"last_success,omitempty"`
	NextCheck     *time.Time  `json:"next_check,omitempty"`
	CheckFailures int         `json:"check_failures,omitempty"`
	Error         string      `json:"error,omitempty"`
	Latest        *Latest     `json:"latest,omitempty"`
	Stage         string      `json:"stage"`
	Target        *ViewTarget `json:"target,omitempty"`
	Operation     *Operation  `json:"operation,omitempty"`
	Blocked       *Blocked    `json:"blocked,omitempty"`
}

// ViewTarget 是待更新的版本。
type ViewTarget struct {
	Version string `json:"version"`
	Kind    string `json:"kind"` // core 或 shell
	Size    int64  `json:"size,omitempty"`
	SHA256  string `json:"sha256"`
}

func instant(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// ViewOf 把狀態整理成四態。優先序：從沒查過 → 已知有待更新版本 → 最近一次查詢失敗 → 已是最新。
// 已知有待更新版本時，即使最近一次查詢離線失敗，仍如實顯示有新版（失敗次數與時間照樣附上）。
func ViewOf(s *State, installed string) UpdateView {
	v := UpdateView{
		Installed: installed, LastCheck: instant(s.LastCheck), LastSuccess: instant(s.LastSuccess), NextCheck: instant(s.NextCheck),
		CheckFailures: s.CheckFailures, Error: s.LastError, Latest: s.Latest, Stage: s.Stage, Blocked: s.Blocked,
	}
	if t := s.Target; t != nil && s.Stage != StageActive && s.Stage != StageFailed {
		v.Target = &ViewTarget{Version: t.Version, Kind: t.Kind, Size: t.Size, SHA256: t.SHA256}
	}
	switch {
	case s.LastCheck.IsZero() && s.LastSuccess.IsZero():
		v.State = CheckNever
	case v.Target != nil:
		v.State = CheckUpdateAvailable
	case s.CheckFailures > 0 || s.LastError != "":
		v.State = CheckFailed
	default:
		v.State = CheckUpToDate
	}
	return v
}
