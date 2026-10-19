//go:build e2etest

package e2esync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var runName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// Pause consumes one explicit control in the dedicated user's e2e-sync directory.
// The ready file is never overwritten. A matching release must arrive within five
// seconds; on timeout callers retain the old writer or reject the test activation.
func Pause(base, point string) error {
	if !runName.MatchString(point) {
		return fmt.Errorf("invalid E2E synchronization point")
	}
	dir := filepath.Join(base, "e2e-sync")
	control := filepath.Join(dir, point+".json")
	b, err := os.ReadFile(control)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var cfg struct {
		Run       string `json:"run"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	if !runName.MatchString(cfg.Run) || cfg.TimeoutMS < 1 || cfg.TimeoutMS > 5000 {
		return fmt.Errorf("invalid E2E synchronization control")
	}
	if err := os.Remove(control); err != nil {
		return err
	}
	stem := filepath.Join(dir, point+"."+cfg.Run)
	f, err := os.OpenFile(stem+".ready.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(f).Encode(map[string]any{"run": cfg.Run, "point": point, "pid": os.Getpid(), "utc": time.Now().UTC().Format(time.RFC3339Nano)})
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	deadline := time.Now().Add(time.Duration(cfg.TimeoutMS) * time.Millisecond)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(stem + ".release")
		if err == nil && string(b) == cfg.Run {
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		time.Sleep(2 * time.Millisecond)
	}
	return fmt.Errorf("E2E synchronization %s/%s timed out", point, cfg.Run)
}
