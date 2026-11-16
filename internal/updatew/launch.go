package updatew

import (
	"fmt"
	"io"
	"path/filepath"
	"time"
)

// Env 是 Launch 需要的平台動作；測試時替換。
type Env struct {
	Base string // 個人資料目錄（%LOCALAPPDATA%\Zuin）
	Now  func() time.Time
	// Hash 回傳檔案的 SHA-256（小寫十六進位）；不存在或讀不到時回傳空字串。
	Hash func(path string) string
	// Exec 執行 path 並等它結束；沒啟動起來時 startErr 不是 nil。
	Exec func(path string, args []string) (startErr error, code int)
	// View 讀 state.json 的查詢紀錄；nil 時用 ReadUpdateView。
	View func() *UpdateView
	Log  io.Writer // 可為 nil
}

// Launch 依紀錄選擇 bin\0c-update.exe 或 bin\0c-update.prev.exe 執行 args（已由呼叫端核對過是三種呼叫之一），
// 記下結果，回傳要交給工作排程器的結束碼。
func Launch(env Env, args []string) int {
	if env.View == nil {
		env.View = func() *UpdateView { return ReadUpdateView(env.Base) }
	}
	logf := func(format string, a ...any) {
		if env.Log != nil {
			fmt.Fprintf(env.Log, "%s 0c-updatew: %s\n", env.Now().Format(time.RFC3339), fmt.Sprintf(format, a...))
		}
	}
	bin := filepath.Join(env.Base, "bin")
	paths := map[string]string{targetStable: filepath.Join(bin, UpdaterName), targetPrev: filepath.Join(bin, PreviousName)}
	f := files{stable: env.Hash(paths[targetStable]), prev: env.Hash(paths[targetPrev])}
	verb := args[0]

	s, err := Load(env.Base)
	if err != nil {
		// 紀錄損毀或是較新的格式：不判斷、不寫回，照舊執行穩定副本（沒啟動起來時仍試上一版）。
		logf("略過退回紀錄：%v", err)
		s = nil
	}
	now := env.Now()
	target, probe := targetStable, false
	var before *UpdateView
	if s != nil {
		s.observe(f, now)
		before = env.View()
		target, probe = s.choose(f, verb, before, now)
		if err := s.save(env.Base); err != nil {
			logf("無法寫入退回紀錄：%v", err)
		}
	}
	switch {
	case probe:
		logf("0c-update 查詢更新已連續失敗超過 %d 小時，這次以上一版（%s）測試", int(probeAfter.Hours()), PreviousName)
	case target == targetPrev:
		logf("0c-update 已判定異常（%s），改執行上一版 %s", s.Fallback.Reason, PreviousName)
	}

	code := 0
	for attempt := 0; attempt < 2; attempt++ {
		startErr, c := env.Exec(paths[target], args)
		code = c
		if startErr != nil {
			code = 1
		}
		outcome, detail := Classify(verb, startErr, c)
		if outcome != OK {
			logf("%s：%s（%s）", filepath.Base(paths[target]), outcome, detail)
		}
		if s == nil {
			if startErr != nil && target == targetStable && f.prevUsable() {
				target = targetPrev
				logf("改執行上一版 %s", PreviousName)
				continue
			}
			break
		}
		now = env.Now()
		// 執行期間別的喚起（登入工作、通知按鈕）可能也更新了紀錄：重新讀過再記，少蓋掉別人的結果。
		if latest, err := Load(env.Base); err == nil {
			s = latest
			s.observe(f, now)
		}
		runOther := s.record(f, verb, target, probe, outcome, detail, startErr == nil, before, env.View(), now)
		if err := s.save(env.Base); err != nil {
			logf("無法寫入退回紀錄：%v", err)
		}
		if !runOther || attempt > 0 {
			break
		}
		if target == targetStable {
			target = targetPrev
		} else {
			target = targetStable
		}
		probe = false
		logf("%s 沒有啟動起來，改執行 %s", filepath.Base(paths[otherOf(target)]), filepath.Base(paths[target]))
	}
	return code
}

func otherOf(target string) string {
	if target == targetStable {
		return targetPrev
	}
	return targetStable
}
