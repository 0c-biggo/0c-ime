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
			s.Stage != StagePrepared || s.Switched || s.failed(version) {
			return ErrTargetChanged
		}
		s.Snooze = &NoticeSnooze{Version: version, SHA256: sha256, Until: now.Add(time.Hour)}
		s.record(now, "notice_snoozed", version, "1h")
		return nil
	})
	return err
}
