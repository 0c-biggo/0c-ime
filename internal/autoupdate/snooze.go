package autoupdate

import "time"

// SnoozeNotice persists an explicit one-hour reminder under the updater lock.
// A stale notification cannot postpone or act on a different target.
func SnoozeNotice(base, version, sha256 string, now time.Time) error {
	unlock, err := lock(base)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = EditState(base, func(s *State) error {
		if s.Target == nil || version == "" || sha256 == "" || s.Target.Version != version || s.Target.SHA256 != sha256 ||
			!s.snoozable() || s.Switched || s.failed(version) {
			return ErrTargetChanged
		}
		s.Snooze = &NoticeSnooze{Version: version, SHA256: sha256, Until: now.Add(time.Hour)}
		s.record(now, "notice_snoozed", version, "1h")
		return nil
	})
	return err
}

// snoozable：通知的「稍後 1 小時」只對等你處理的那一版有效：已下載等安裝（prepared），或手動更新查到、等你按
// 「立即下載」的新版（found＋needs_download，#340）。
func (s *State) snoozable() bool {
	return s.Stage == StagePrepared || s.Stage == StageFound && s.Blocked != nil && s.Blocked.Reason == "needs_download"
}

// NoticeDelivered 記下使用者親自操作後、更新器當場送出的待辦通知（手動更新按「立即下載」後的「立即安裝」，#340），
// 背景提醒接著同一個計數往後排（約 24、72 小時），不在下一次排程又送一則同樣的。版本或資產已變時不記。
func NoticeDelivered(base string, n Notice, now time.Time) error {
	_, err := EditState(base, func(s *State) error {
		if s.Target == nil || s.Blocked == nil || s.Target.Version != n.TargetVersion || s.Target.SHA256 != n.TargetSHA256 {
			return nil
		}
		key := s.Target.Version + "/" + s.Blocked.Reason
		if s.NoticeFor != key {
			s.NoticeFor, s.NoticeCount = key, 0
		}
		s.NoticeCount++
		s.LastNotice = now
		s.record(now, "notified", s.Target.Version, s.Blocked.Reason)
		return nil
	})
	return err
}
