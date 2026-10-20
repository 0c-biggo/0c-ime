package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const (
	stateFile     = "state.json"
	operationFile = "operation.json"
)

// LastError 是上一次下載或配對失敗的原因（status 的 last_error）。
type LastError struct {
	Code    string    `json:"code"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

type state struct {
	Schema    int        `json:"schema"`
	LastError *LastError `json:"last_error,omitempty"`
}

// writeJSON 先寫暫存檔再改名，讀的人不會讀到寫一半的檔案。
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		os.Remove(tmp.Name())
	}
	return werr
}

// LastError 讀上一次的錯誤；沒有時為 nil。
func (s Store) LastError() *LastError {
	var st state
	if b, err := os.ReadFile(filepath.Join(s.Root, stateFile)); err == nil && json.Unmarshal(b, &st) == nil {
		return st.LastError
	}
	return nil
}

// SetLastError 記下錯誤；err 為 nil 時清掉。
func (s Store) SetLastError(err error, now time.Time) error {
	if err == nil {
		rmErr := os.Remove(filepath.Join(s.Root, stateFile))
		if os.IsNotExist(rmErr) {
			return nil
		}
		return rmErr
	}
	e := AsError(err)
	return writeJSON(filepath.Join(s.Root, stateFile), state{Schema: 1, LastError: &LastError{Code: e.Code, Message: e.Message, At: now.UTC().Truncate(time.Second)}})
}

// Operation 是進行中的下載（status 的 operation）。PID 是下載的行程，status 只在它還握著鎖時採信。
type Operation struct {
	PID       int       `json:"pid"`
	Phase     string    `json:"phase"`
	DoneBytes int64     `json:"done_bytes"`
	Total     int64     `json:"total_bytes"`
	StartedAt time.Time `json:"started_at"`
}

// WriteOperation 記下進度。
func (s Store) WriteOperation(op Operation) error {
	return writeJSON(filepath.Join(s.Root, operationFile), op)
}

// ReadOperation 讀進度檔；沒有時 ok 為 false（是否仍在進行由呼叫端核對 PID 與鎖）。
func (s Store) ReadOperation() (Operation, bool) {
	var op Operation
	b, err := os.ReadFile(filepath.Join(s.Root, operationFile))
	if err != nil || json.Unmarshal(b, &op) != nil || op.PID <= 0 {
		return Operation{}, false
	}
	return op, true
}

// ClearOperation 刪掉進度檔。
func (s Store) ClearOperation() { os.Remove(filepath.Join(s.Root, operationFile)) }
