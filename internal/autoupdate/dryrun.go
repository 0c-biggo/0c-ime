package autoupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// errDryRun：試算時不下載、不安裝、不動引擎。
var errDryRun = errors.New("試算不執行這一步")

// DryRunInputs 是試算用的本機資訊（已安裝版本、平台）。
type DryRunInputs struct {
	Installed string // 已安裝（引擎指標或收據）的版本
	Shell     string // 完整安裝（外殼）的版本，依版本規則選資產用（#295）；空字串時同 Installed
	Updater   string // 執行試算的 0c-update 版本；開發建置為空字串
	GOOS      string
	GOARCH    string
}

// dryPlatform 只回答查詢與已安裝版本；會改動任何東西的步驟一律回 errDryRun。
type dryPlatform struct {
	feed *update.SignedFeed
	in   DryRunInputs
}

func (p *dryPlatform) Installed() (string, error) { return p.in.Installed, nil }
func (p *dryPlatform) Running(context.Context) (string, bool, error) {
	return "", false, nil
}
func (p *dryPlatform) Fetch(context.Context, uint64) (*update.SignedFeed, error) { return p.feed, nil }
func (p *dryPlatform) Download(context.Context, Target, string) (string, error) {
	return "", errDryRun
}
func (p *dryPlatform) Prepare(context.Context, string, Target) (string, error) { return "", errDryRun }
func (p *dryPlatform) SwitchCore(string, string) error                         { return errDryRun }
func (p *dryPlatform) RevertCore(string) error                                 { return errDryRun }
func (p *dryPlatform) Drain(context.Context) error                             { return errDryRun }
func (p *dryPlatform) StartEngine(context.Context) error                       { return errDryRun }
func (p *dryPlatform) InstallShell(context.Context, string) (bool, error)      { return false, errDryRun }
func (p *dryPlatform) Conditions() Conditions                                  { return Conditions{} }
func (p *dryPlatform) Notify(Notice) error                                     { return nil }
func (p *dryPlatform) Target() (string, string)                                { return p.in.GOOS, p.in.GOARCH }
func (p *dryPlatform) UpdaterVersion() string                                  { return p.in.Updater }
func (p *dryPlatform) ShellVersion() string {
	if p.in.Shell != "" {
		return p.in.Shell
	}
	return p.in.Installed
}

// DryRun 以已驗證的索引試算協調器下一次查詢會怎麼決定（#293 的 selftest）：在 s 的複本上執行與 auto 相同的
// 查詢判斷（資格、撤回、分批、取代舊目標），回傳試算後的狀態。不寫狀態檔、不下載、不安裝、不碰引擎，也不拿鎖：
// 新更新器在換上穩定副本之前用它證明自己讀得懂現有狀態與線上索引，不會一跑就當掉。
func DryRun(ctx context.Context, s *State, feed *update.SignedFeed, in DryRunInputs, now time.Time) (*State, error) {
	if s == nil || feed == nil {
		return nil, fmt.Errorf("缺少狀態或索引")
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var copy State
	if err = json.Unmarshal(b, &copy); err != nil {
		return nil, err
	}
	c := &Coordinator{Platform: &dryPlatform{feed: feed, in: in}, Now: func() time.Time { return now }, Jitter: func(time.Duration) time.Duration { return 0 }, dryRun: true}
	c.defaults()
	c.discover(ctx, &copy, now)
	return &copy, nil
}

// Describe 是試算結果的一句話說明（selftest 的輸出）。
func Describe(s *State) string {
	switch {
	case s == nil:
		return ""
	case s.LastError != "":
		return "查詢失敗：" + s.LastError
	case s.Target != nil:
		detail := fmt.Sprintf("下一步：%s %s（%s）", s.Stage, s.Target.Version, s.Target.Kind)
		if s.Blocked != nil {
			detail += "；待處理：" + s.Blocked.Reason
		}
		return detail
	case s.Latest != nil && !s.Latest.Eligible && s.Latest.Reason != "":
		return fmt.Sprintf("索引最新 %s，這台電腦暫不更新（%s）", s.Latest.Version, s.Latest.Reason)
	case s.Latest != nil:
		return fmt.Sprintf("索引最新 %s，沒有要做的事", s.Latest.Version)
	}
	return "沒有要做的事"
}
