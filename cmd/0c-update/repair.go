package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"git.biggo.com/0c/ime/internal/autoupdate"
)

// 防毒軟體（例如 Defender 的雲端 ML 判定）可能把穩定更新器 bin/0c-update 連同排程一起隔離，之後自動更新就靜默停止
// （#22：2026-09-30 dev.18 被判為 Trojan:Win32/Bearfoos.A!ml）。0c-ui 定期以版本目錄內的 0c-update 執行
// schedule repair：缺少時重建一次；重建後 repairWindow 內又被移除，表示同一個檔案會再被隔離，停止重建並通知使用者。
const repairWindow = 7 * 24 * time.Hour

// AntivirusHelpURL 是防毒軟體誤判的說明頁（內容來源 docs/antivirus.md）。
const AntivirusHelpURL = "https://0c.biggo.com/help/antivirus/"

const (
	problemUpdaterMissing  = "updater_missing"
	problemScheduleMissing = "schedule_missing"
)

func problemText(p string) string {
	switch p {
	case problemUpdaterMissing:
		return "自動更新程式被移除"
	case problemScheduleMissing:
		return "自動更新排程被移除"
	}
	return p
}

// repairHooks 是 repairUpdater 需要的平台動作；測試時替換。
type repairHooks struct {
	scheduleExists func() bool
	install        func(base string) error
	refreshStable  func(base string) (bool, error) // 穩定副本不是目前版本時換掉（見 refreshStaleUpdater）；nil 表示不處理
	notify         func(autoupdate.Notice) error
	now            func() time.Time
}

var platformRepair = repairHooks{scheduleExists: scheduleExists, install: installSchedule, refreshStable: refreshStableUpdater,
	notify: notify, now: time.Now}

// scheduleWanted：完整安裝建立過排程（有 bin/），且使用者沒有以 schedule remove 關閉。
func scheduleWanted(base string, s *autoupdate.State) bool {
	info, err := os.Stat(filepath.Join(base, "bin"))
	return err == nil && info.IsDir() && !s.ScheduleRemoved
}

func updaterProblem(base string, s *autoupdate.State, h repairHooks) string {
	if !scheduleWanted(base, s) {
		return ""
	}
	if _, err := os.Stat(stableUpdater(base)); err != nil {
		return problemUpdaterMissing
	}
	if !h.scheduleExists() {
		return problemScheduleMissing
	}
	return ""
}

// repairUpdater 檢查並在需要時重建排程與穩定更新器；無法自動處理時回傳錯誤，report 時以通知告知使用者（每 7 天至多一次）。
//
// 先讀一份快照決定要做什麼；換檔、重建排程與通知都可能花上數秒（複製更新器、防毒掃描），在鎖外做，結果再以
// EditState 短暫上鎖寫回，不覆寫協調器在這段時間存下的進度與紀錄。
func repairUpdater(base string, report bool, h repairHooks) error {
	s, err := autoupdate.Load(base)
	if err != nil {
		return err
	}
	// 穩定副本過舊不是「被移除」：不計入重建次數，也不通知使用者。
	refreshKind, refreshDetail := "", ""
	if h.refreshStable != nil && scheduleWanted(base, s) {
		switch ok, err := h.refreshStable(base); {
		case err != nil:
			refreshKind, refreshDetail = "updater_refresh_failed", err.Error()
		case ok:
			refreshKind = "updater_refreshed"
			fmt.Println("已把自動更新程式換成目前版本")
		}
	}
	recordRefresh := func(s *autoupdate.State) {
		// 換新一直失敗（例如舊檔一直被占用）時每次 repair 都走到這裡；同樣的失敗只記一次，不把其他紀錄擠出歷史。
		if refreshKind == "" || refreshKind == "updater_refresh_failed" && lastRefresh(s) == refreshKind+"\t"+refreshDetail {
			return
		}
		s.Record(refreshKind, refreshDetail)
	}
	problem := updaterProblem(base, s, h)
	if problem == "" {
		if s.UpdaterBroken == "" && refreshKind == "" {
			return nil
		}
		_, err = autoupdate.EditState(base, func(s *autoupdate.State) error {
			recordRefresh(s)
			if s.UpdaterBroken != "" {
				s.UpdaterBroken = ""
				s.Record("updater_ok", "")
			}
			return nil
		})
		return err
	}
	now := h.now()
	recent := false
	for _, t := range s.UpdaterRepairs {
		recent = recent || now.Sub(t) < repairWindow
	}
	installed := false
	var installErr error
	if !recent {
		if installErr = h.install(base); installErr == nil {
			installed = true
			fmt.Println("已重建自動更新（原因：" + problemText(problem) + "）")
		}
	}
	var notified time.Time // 這次先占下的通知時間；通知失敗時還原成 previous
	var previous time.Time
	_, err = autoupdate.EditState(base, func(s *autoupdate.State) error {
		recordRefresh(s)
		kept := s.UpdaterRepairs[:0]
		for _, t := range s.UpdaterRepairs {
			if now.Sub(t) < repairWindow {
				kept = append(kept, t)
			}
		}
		s.UpdaterRepairs = kept
		if installed {
			s.UpdaterRepairs = append(s.UpdaterRepairs, now)
			s.UpdaterBroken = ""
			s.Record("updater_repaired", problem)
			return nil
		}
		if installErr != nil {
			s.Record("updater_repair_failed", installErr.Error())
		}
		s.UpdaterBroken = problem
		if report && (s.UpdaterNotified.IsZero() || now.Sub(s.UpdaterNotified) >= repairWindow) {
			// 在鎖內先占下，兩個同時執行的 repair 不會各通知一次。
			previous, notified = s.UpdaterNotified, now
			s.UpdaterNotified = now
		}
		return nil
	})
	if err != nil || installed {
		return err
	}
	if !notified.IsZero() {
		n := autoupdate.Notice{Title: "靈犀注音的自動更新已停止",
			Body: problemText(problem) + "，可能是防毒軟體誤判。輸入法仍可正常使用，但不會再自動更新。處理方式：" + AntivirusHelpURL}
		if h.notify(n) != nil {
			_, err = autoupdate.EditState(base, func(s *autoupdate.State) error {
				if s.UpdaterNotified.Equal(notified) {
					s.UpdaterNotified = previous // 沒通知成功，下次再試
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("自動更新已停止：%s（說明：%s）", problemText(problem), AntivirusHelpURL)
}

// lastRefresh 回傳最近一筆換新紀錄的「種類\t說明」；沒有時為空字串。
func lastRefresh(s *autoupdate.State) string {
	for i := len(s.History) - 1; i >= 0; i-- {
		if e := s.History[i]; e.Kind == "updater_refreshed" || e.Kind == "updater_refresh_failed" {
			return e.Kind + "\t" + e.Detail
		}
	}
	return ""
}
