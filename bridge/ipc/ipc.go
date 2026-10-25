// Package ipc 是 0c-server 與平台薄殼之間的傳輸層（docs/19）。
//
// 訊框：4 位元組小端序長度 + JSON 內容。薄殼送 Envelope，server 回 Reply；一問一答、同步。
// 每個連線各自擁有它開的 session，連線關閉時一併關閉。
package ipc

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/core/proto"
)

// MaxFrame 是單一訊框的上限；超過即視為協定錯誤並斷線。
const MaxFrame = 1 << 20

// 操作。
const (
	OpOpen          = "open"           // 開一個 session，回傳 Session
	OpClose         = "close"          // 關閉 Session
	OpHandle        = "handle"         // 在 Session 上處理 Req，回傳 Snapshot
	OpPing          = "ping"           // 健康檢查
	OpHealth        = "health"         // 運作版本與狀態（不含文字），供更新協調器與 E2E 核對
	OpApplySettings = "apply_settings" // 等所有輸入框組字完成後套用設定，不因等待逾時取消
	OpDrain         = "drain"          // 更新交接：沒有組字時存檔並退出，外殼重連時改接新版引擎
	OpVocab         = "vocab"          // 自訂詞彙管理指令（#122）：Vocab 進、Vocab 出；不需要 session
)

// Envelope 是薄殼送出的訊息。
type Envelope struct {
	Op      string               `json:"op"`
	Session uint64               `json:"session,omitempty"`
	Req     *proto.Request       `json:"req,omitempty"`
	Version int                  `json:"version,omitempty"` // 協定版本；open 時檢查
	Vocab   *engine.VocabRequest `json:"vocab,omitempty"`
}

// Reply 是 server 的回應。
type Reply struct {
	OK       bool                  `json:"ok"`
	Error    string                `json:"error,omitempty"`
	Session  uint64                `json:"session,omitempty"`
	Snapshot *proto.Snapshot       `json:"snapshot,omitempty"`
	EngineUS int64                 `json:"engine_us,omitempty"` // 含核心互斥鎖等待；不含外殼 IPC／呈現
	Info     map[string]string     `json:"info,omitempty"`      // 開 session 時提供非個人化的版本識別
	Vocab    *engine.VocabResponse `json:"vocab,omitempty"`
}

// WriteFrame 寫出一個訊框。
func WriteFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > MaxFrame {
		return fmt.Errorf("訊框 %d 位元組超過上限", len(b))
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// ReadFrame 讀一個訊框並解碼到 v。
func ReadFrame(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.LittleEndian.Uint32(hdr[:])
	if n > MaxFrame {
		return errors.New("訊框超過上限")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
