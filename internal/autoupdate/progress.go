package autoupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Operation is a live update operation, separate from the durable transaction.
// Download bytes describe received data; verification and installation are separate phases.
type Operation struct {
	PID         int       `json:"pid"`
	Version     string    `json:"version,omitempty"`
	SHA256      string    `json:"sha256,omitempty"`
	Phase       string    `json:"phase"`
	Received    int64     `json:"received"`
	Total       int64     `json:"total"`
	Interactive bool      `json:"interactive"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func writeRuntime(base, name string, value any) error {
	dir := filepath.Join(base, "update")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".operation-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(b)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(dir, name))
}

func WriteOperation(base string, op Operation) error {
	op.PID, op.UpdatedAt = os.Getpid(), time.Now()
	return writeRuntime(base, "operation.json", op)
}

func ClearOperation(base string) { os.Remove(filepath.Join(base, "update", "operation.json")) }

// Only the current coordinator can report a live operation. A crashed process,
// old target or leftover progress file must not leave a permanent progress bar.
func ReadOperation(base string, target *Target) *Operation {
	b, err := os.ReadFile(filepath.Join(base, "update", "operation.json"))
	if err != nil {
		return nil
	}
	var op Operation
	if json.Unmarshal(b, &op) != nil || op.PID <= 0 || !processAlive(op.PID) {
		return nil
	}
	b, err = os.ReadFile(filepath.Join(base, "update", "coordinator.lock"))
	var owner int
	if err != nil {
		return nil
	}
	fmt.Sscan(string(b), &owner)
	if owner != op.PID {
		return nil
	}
	if op.Phase != "checking" && (target == nil || op.Version != target.Version || op.SHA256 != target.SHA256) {
		return nil
	}
	if op.Total < 0 || op.Received < 0 || op.Received > op.Total {
		return nil
	}
	return &op
}

func (c *Coordinator) phase(s *State, phase string) {
	op := Operation{Phase: phase}
	if s.Target != nil {
		op.Version, op.SHA256, op.Total = s.Target.Version, s.Target.SHA256, s.Target.Size
		if phase == "preparing" || phase == "installing" {
			op.Received = op.Total
		}
	}
	// Observability never bypasses transaction verification or prevents updates.
	_ = WriteOperation(c.Base, op)
}

type downloadRequest struct {
	Version string    `json:"version"`
	SHA256  string    `json:"sha256"`
	Until   time.Time `json:"until"`
}

func RequestDownload(base string, t Target) error {
	return writeRuntime(base, "download-request.json", downloadRequest{t.Version, t.SHA256, time.Now().Add(30 * time.Minute)})
}

func DownloadRequested(base string, t Target) bool {
	b, err := os.ReadFile(filepath.Join(base, "update", "download-request.json"))
	var r downloadRequest
	return err == nil && json.Unmarshal(b, &r) == nil && r.Version == t.Version && r.SHA256 == t.SHA256 && time.Now().Before(r.Until)
}

// DownloadNow prepares a verified target immediately without installing it.
// An existing download accepts an acceleration request instead of starting a second download.
func (c *Coordinator) DownloadNow(ctx context.Context, version, sha256 string) (*State, error) {
	c.defaults()
	unlock, err := waitLock(ctx, c.Base, "coordinator.lock")
	if err != nil {
		if !errors.Is(err, ErrLocked) {
			return nil, err
		}
		s, loadErr := Load(c.Base)
		if loadErr != nil {
			return nil, loadErr
		}
		if s.Target == nil || s.Target.Version != version || s.Target.SHA256 != sha256 {
			return s, ErrTargetChanged
		}
		op := ReadOperation(c.Base, s.Target)
		if op == nil || (op.Phase != "downloading" && op.Phase != "retrying" && op.Phase != "verifying") {
			return s, ErrUpdateInProgress
		}
		return s, RequestDownload(c.Base, *s.Target)
	}
	defer unlock()
	defer ClearOperation(c.Base)
	s, err := Load(c.Base)
	if err != nil {
		return nil, err
	}
	if s.Switched || s.RestartPending {
		return s, ErrUpdateInProgress
	}
	if s.Target == nil || s.Target.Version != version || s.Target.SHA256 != sha256 {
		return s, ErrTargetChanged
	}
	c.discover(ctx, s, c.Now())
	if s.LastError != "" {
		return s, errors.Join(fmt.Errorf("無法確認更新版本：%s", s.LastError), c.save(s))
	}
	if s.Target == nil || s.Target.Version != version || s.Target.SHA256 != sha256 || s.failed(version) {
		return s, errors.Join(ErrTargetChanged, c.save(s))
	}
	if s.Stage == StagePending {
		return s, c.save(s)
	}
	if err := RequestDownload(c.Base, *s.Target); err != nil {
		return s, err
	}
	if s.Stage == StageFound {
		c.download(ctx, s, c.Now())
	}
	if s.Stage == StageDownloaded {
		c.prepare(ctx, s, c.Now())
	}
	if s.Stage == StagePrepared {
		s.block(c.Now(), "needs_consent", "已下載，按立即安裝完成更新")
	}
	return s, c.save(s)
}
