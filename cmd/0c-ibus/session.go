//go:build linux

package main

// 0c-server 用戶端與每個輸入欄位的 session；行為與 Fcitx5 外殼的 client.cpp／session.cpp 相同：
// 連不上就啟動 0c-server，斷線後重開 session 並重播這次組字以來的請求。

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"git.biggo.com/0c/ime/bridge/ipc"
	"git.biggo.com/0c/ime/core/proto"
)

// requestTimeout 是單次請求的上限；超過就放棄，按鍵交還應用程式，不得卡住宿主。
const requestTimeout = 2 * time.Second

type client struct {
	socket, legacy, server string
	conn                   net.Conn
	generation             uint64
	startWait              time.Duration // 啟動 0c-server 後等它接受連線的上限
}

func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return "/tmp"
}

func newClient() *client {
	server := "0c-server"
	if home := os.Getenv("HOME"); home != "" {
		p := filepath.Join(home, ".local/bin/0c-server")
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			server = p
		}
	}
	// 升級過渡期：舊版 zuin-server 仍在執行時它是個人詞庫唯一的寫入者，先接上它。
	return &client{socket: filepath.Join(runtimeDir(), "0c/0c.sock"),
		legacy: filepath.Join(runtimeDir(), "zuin/zuin.sock"), server: server, startWait: 2 * time.Second}
}

func (c *client) connectOnce() bool {
	for _, p := range []string{c.socket, c.legacy} {
		if p == "" {
			continue
		}
		if conn, err := net.DialTimeout("unix", p, requestTimeout); err == nil {
			c.conn = conn
			c.generation++
			return true
		}
	}
	return false
}

func (c *client) spawnServer() {
	cmd := exec.Command(c.server)
	cmd.Stdin, cmd.Stdout = nil, nil
	cmd.Stderr = os.Stderr
	// 自成工作階段，不隨 ibus-daemon 的行程群組收到訊號。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cmd.Start() == nil {
		go cmd.Wait()
	}
}

func (c *client) connect() bool {
	if c.conn != nil {
		return true
	}
	if c.connectOnce() {
		return true
	}
	c.spawnServer()
	for waited := time.Duration(0); waited < c.startWait; waited += 50 * time.Millisecond { // server 要載入資料包
		time.Sleep(50 * time.Millisecond)
		if c.connectOnce() {
			return true
		}
	}
	return false
}

func (c *client) close() {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// call 送出一個 Envelope 並讀回 Reply；失敗時關閉連線，下一次呼叫重新連線。
func (c *client) call(env ipc.Envelope) (ipc.Reply, bool) {
	var reply ipc.Reply
	if !c.connect() {
		return reply, false
	}
	c.conn.SetDeadline(time.Now().Add(requestTimeout))
	if ipc.WriteFrame(c.conn, env) != nil || ipc.ReadFrame(c.conn, &reply) != nil {
		c.close()
		return ipc.Reply{}, false
	}
	return reply, true
}

// snapshot 是外殼看到的一次結果。ok=false 時 server 不可用，按鍵放行。
type snapshot struct {
	proto.Snapshot
	ok, reconnected bool
}

func (s snapshot) english() bool { return s.Mode == proto.ModeEnglish }

func (s snapshot) candidates() []proto.CandidateItem {
	if s.Candidates == nil {
		return nil
	}
	return s.Candidates.Items
}

// replayView 是判斷重播結果是否與斷線前畫面相同的欄位。
type replayView struct {
	Preedit        []proto.Segment       `json:"preedit,omitempty"`
	Cursor         proto.CursorPos       `json:"cursor"`
	Candidates     *proto.CandidateList  `json:"candidates,omitempty"`
	Suggestion     string                `json:"suggestion,omitempty"`
	SuggestionEdit *proto.SuggestionEdit `json:"suggestion_edit,omitempty"`
	Mode           string                `json:"mode"`
}

func viewOf(s proto.Snapshot) []byte {
	b, _ := json.Marshal(replayView{s.Preedit, s.Cursor, s.Candidates, s.Suggestion, s.SuggestionEdit, s.Mode})
	return b
}

type session struct {
	client                 *client
	documentContextEnabled bool

	id, generation uint64
	info           map[string]string // open 回覆的引擎資訊；重開 session 時更新
	log            []proto.Request
	cleared        []proto.Request // 切到英文清空組字前的日誌；只給緊接著的放回切換接續（#90）
	toggled        bool            // 上一個操作是成功的切換；見 toggle
	view           []byte          // 斷線前最後顯示的組字；沒有組字時為 nil
	shown          proto.Snapshot  // view 對應的快照，失焦時提交它
	recovered      *proto.Snapshot // 重播結果與斷線前不同，等使用者確認

	private, english, englishPending bool
}

func newSession(c *client, documentContext bool) *session {
	return &session{client: c, documentContextEnabled: documentContext}
}

func (s *session) setPrivate(v bool) { s.private = v }

// shiftKeys 是中英切換鍵（health 的 left_shift、right_shift、shift_tap_ms，#117）。
type shiftKeys struct {
	left, right bool
	tap         time.Duration // 單按門檻：按住超過就不算切換
}

const defaultShiftTap = 300 * time.Millisecond

// shiftKeysFrom 解讀引擎資訊。舊引擎沒有某一項時：左 Shift 交給應用程式、右 Shift 照舊切換、門檻 300 ms。
func shiftKeysFrom(info map[string]string) shiftKeys {
	k := shiftKeys{left: info["left_shift"] == "true", right: info["right_shift"] != "false", tap: defaultShiftTap}
	if ms, err := strconv.Atoi(info["shift_tap_ms"]); err == nil && ms > 0 {
		k.tap = time.Duration(ms) * time.Millisecond
	}
	return k
}

// shiftKeys 每次向引擎查詢，設定套用（引擎重啟）後立即生效。套用設定會重啟引擎，舊連線在這一次才發現斷了，
// 所以失敗時重連再問一次，才不會照預設值切換（例如剛關掉右 Shift）。引擎不可用時照預設值。
func (s *session) shiftKeys() shiftKeys {
	reply, ok := s.client.call(ipc.Envelope{Op: ipc.OpHealth})
	if !ok {
		reply, ok = s.client.call(ipc.Envelope{Op: ipc.OpHealth})
	}
	if !ok || !reply.OK {
		return shiftKeysFrom(nil)
	}
	return shiftKeysFrom(reply.Info)
}

// toggleKey 是 open 回覆的 toggle_key（right、both、left、none），選單的注／英項目據此說明切換鍵；
// 還沒連上或舊引擎沒有這一項時為空字串。
func (s *session) toggleKey() string { return s.info["toggle_key"] }

// vocabKeys 回報引擎是否認得 0.2.0 的加詞與反悔鍵名（open 回覆的 vocab_keys，#123）。值隨 session 快取，
// 引擎重啟（例如套用設定）後重開 session 時更新，不必每個按鍵查 health。斷線要等下一個請求才發現，
// 引擎剛重啟、還沒有任何請求時沿用舊值：舊值 false 只是這一鍵照原本的鍵送；舊值 true 送到新引擎，
// 開關關閉時它把新鍵名當原本的鍵處理。引擎不可用或舊版引擎沒有這一項時為 false。
func (s *session) vocabKeys() bool {
	return s.client.connect() && s.ensureOpen() && s.info["vocab_keys"] == "true"
}

func (s *session) contextAllowed() bool {
	return s.documentContextEnabled && !s.private && !s.english
}

func (s *session) close() {
	if s.id != 0 && s.generation == s.client.generation {
		s.client.call(ipc.Envelope{Op: ipc.OpClose, Session: s.id})
	}
	s.id = 0
}

func (s *session) do(req proto.Request) (ipc.Reply, bool) {
	r, ok := s.client.call(ipc.Envelope{Op: ipc.OpHandle, Session: s.id, Req: &req})
	return r, ok && r.OK
}

func (s *session) ensureOpen() bool {
	if s.id != 0 && s.generation == s.client.generation {
		return true
	}
	reply, ok := s.client.call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
	if !ok || !reply.OK {
		s.id = 0
		return false
	}
	s.id, s.generation, s.info = reply.Session, s.client.generation, reply.Info
	// GNOME 的候選窗是原生直排，沒有格狀版面；核心的上下鍵導覽要與畫面一致。
	if _, ok := reply.Info["candidate_layout"]; ok {
		if _, ok := s.do(proto.Request{Type: proto.Display, Columns: 1}); !ok {
			s.id = 0
			return false
		}
	}
	// 能確認應用程式文件更新才啟用跨提交協定；舊 server 會安全忽略。
	if s.documentContextEnabled {
		if _, ok := s.do(proto.Request{Type: proto.Context, ContextEnabled: true}); !ok {
			s.id = 0
			return false
		}
	}
	// 重播：新 session 回到斷線前的組字狀態。
	var last *proto.Snapshot
	s.recovered = nil
	for _, req := range s.log {
		if s.private {
			req.Private = true
		}
		r, ok := s.do(req)
		if !ok {
			s.id = 0
			return false
		}
		last = r.Snapshot
	}
	// 神經逾時、模型版本或個人資料變動可能讓同一串按鍵重播成不同文字；
	// 先顯示恢復結果，不在使用者看見之前執行 Enter 或依候選 ID 選字。
	if len(s.log) > 0 && s.view != nil && last != nil && !bytes.Equal(viewOf(*last), s.view) {
		s.recovered = last
	}
	return true
}

func (s *session) key(k string) snapshot { return s.handle(proto.Request{Type: proto.Key, Key: k}) }

// toggle 切換中英。上一次切換之後沒有任何按鍵或其他請求時帶 Restore，
// 核心才放回切到英文時清掉的未打完注音（#90 防誤觸）。
func (s *session) toggle() snapshot {
	snap := s.handle(proto.Request{Type: proto.Toggle, Restore: s.toggled})
	s.toggled = snap.ok
	return snap
}

// bypassEnglish：交還應用程式的 Ctrl＋Home／End、Ctrl＋Shift+Tab 在放行前同步英文 token 邊界；
// 一般快捷鍵只交付 pending 英文。這個入口不得改動注音組字：有組字（英文模式保留的中文）時
// 核心的 Home／End 會移動組字游標，所以只在沒有組字或組字就是 pending 英文時才送邊界鍵。
func (s *session) bypassEnglish(boundary string) snapshot {
	if s.english && boundary != "" && (s.englishPending || s.view == nil) {
		return s.key(boundary)
	}
	if s.englishPending {
		return s.handle(proto.Request{Type: proto.Commit})
	}
	return snapshot{}
}

// commitForShortcut：組字中按 Ctrl／Alt／Super 組合鍵時先送出組字，再把原鍵交給應用程式（#106），
// 聊天軟體的 Ctrl+Enter、Ctrl+V 才不會先執行快捷鍵、組字後送。沒有組字時不送請求。
func (s *session) commitForShortcut() snapshot {
	if s.view == nil && !s.englishPending {
		return snapshot{}
	}
	return s.handle(proto.Request{Type: proto.Commit})
}

// activate：換到這個輸入框時回到開始時的中英模式（mode-memory=native；start-mode，#160，舊引擎沒有回報時是注音）。
// 「所有輸入框共用中英模式」（shared，#115）時一律送「設成中文」，核心改成回到共用的模式，注／英指示立刻是共用的模式，
// 不必等第一個按鍵。焦點事件不為此啟動 0c-server：引擎不在而且目前是注音時不動，第一個按鍵開的 session 本來就從開始時的
// 模式起；目前是英文時照舊連線（以前就會為了回到注音而連線）。
func (s *session) activate() snapshot {
	if !(s.client.conn != nil && s.ensureOpen()) && (!s.english || !s.client.connect() || !s.ensureOpen()) {
		return snapshot{}
	}
	if s.info["mode_memory"] == "shared" {
		return s.handle(proto.Request{Type: proto.Mode})
	}
	start := s.info["start_mode"] == proto.ModeEnglish
	if s.english == start {
		return snapshot{}
	}
	return s.handle(proto.Request{Type: proto.Mode, On: start})
}

// documentContext 確認應用程式已插入剛提交的文字；連線改變即失效。
func (s *session) documentContext(token uint64) bool {
	if !s.documentContextEnabled || s.id == 0 {
		return false
	}
	r := s.handle(proto.Request{Type: proto.Context, On: token != 0, ContextID: token})
	return r.ok && !r.reconnected
}

func (s *session) handle(req proto.Request) snapshot {
	before := s.client.generation
	wire := req
	if s.private {
		wire.Private = true
	}
	// 前文確認與欄數不是使用者操作；其他請求都讓下一次切換不再還原注音。
	var cleared []proto.Request
	if req.Type != proto.Context && req.Type != proto.Display {
		s.toggled = false
		cleared, s.cleared = s.cleared, nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		// 先確認連線：若因此重新連線，代數改變，ensureOpen 會重開 session 並重播。
		if !s.client.connect() || !s.ensureOpen() {
			continue
		}
		if s.recovered != nil {
			switch req.Type {
			case proto.Context:
				// 文件確認不是可見畫面更新；保留復原提示給下一次真正操作。
				return snapshot{}
			case proto.Cancel:
				s.recovered = nil
			case proto.Commit:
				// 失焦時不能等待下一鍵；提交斷線前實際顯示的文字，取消重播狀態而不學習新猜測。
				if _, ok := s.do(proto.Request{Type: proto.Cancel}); !ok {
					s.id = 0
				}
				out := snapshot{ok: true, reconnected: true}
				out.Handled, out.Commit = true, s.shown.Text()
				s.log, s.view, s.recovered = nil, nil, nil
				return out
			}
		}
		if s.recovered != nil {
			snap := *s.recovered
			s.recovered = nil
			snap.Handled, snap.Commit = true, ""
			snap.Notices = []string{recoveryNotice}
			s.view, s.shown = viewOf(snap), snap
			s.english, s.englishPending = snap.Mode == proto.ModeEnglish, snap.EnglishPending
			return snapshot{Snapshot: snap, ok: true, reconnected: true}
		}
		if req.Type == proto.Context && before != s.client.generation {
			wire.On, wire.ContextID = false, 0
		}
		reply, ok := s.client.call(ipc.Envelope{Op: ipc.OpHandle, Session: s.id, Req: &wire})
		if !ok {
			continue // 連線斷了：下一輪重連並重播
		}
		if !reply.OK || reply.Snapshot == nil {
			s.id = 0 // server 不認得這個 session（例如 server 重啟過）：重開並重播
			continue
		}
		out := snapshot{Snapshot: *reply.Snapshot, ok: true, reconnected: before != s.client.generation}
		s.english, s.englishPending = out.english(), out.EnglishPending
		// 符號選單可以在空白組字區開啟，也必須重播才能保留選單與反白位置。
		composing := len(out.Preedit) > 0 || len(out.candidates()) > 0
		if out.Commit != "" || req.Type == proto.Cancel || !composing {
			// #90：切到英文清掉未打完的注音而結束組字時留下這段日誌；立即切回放回注音時接上，重播才得到同樣的組字。
			if req.Type == proto.Toggle && out.Commit == "" && len(s.log) > 0 {
				s.cleared = append(s.log, wire)
			}
			s.log, s.view = nil, nil
		} else if req.Type != proto.Context {
			if len(s.log) == 0 && req.Type == proto.Toggle {
				s.log = cleared
			}
			s.log = append(s.log, wire)
		}
		if composing {
			s.view, s.shown = viewOf(out.Snapshot), out.Snapshot
		}
		return out
	}
	return snapshot{}
}
