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
	"strings"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/internal/installs"
)

// requestTimeout 是單次請求的上限；超過就放棄，按鍵交還應用程式，不得卡住宿主。
const requestTimeout = 2 * time.Second

type client struct {
	socket, legacy, server string
	serverArgs             []string // 系統安裝的引擎帶 -data（internal/installs）
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
	// 共用規則（#181）：先找官網安裝的 ~/.local/bin/0c-server，再找系統安裝，都沒有時用 PATH。
	server, args := installs.Default().Server()
	// 升級過渡期：舊版 zuin-server 仍在執行時它是個人詞庫唯一的寫入者，先接上它。
	return &client{socket: filepath.Join(runtimeDir(), "0c/0c.sock"),
		legacy: filepath.Join(runtimeDir(), "zuin/zuin.sock"), server: server, serverArgs: args, startWait: 2 * time.Second}
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
	cmd := exec.Command(c.server, c.serverArgs...)
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

// alive 回報連線是否還在：不送請求、不等待，只看另一端關了沒有（引擎退出後讀到 EOF）。
// 斷了、或有不該有的殘留資料（訊框會錯位）就關掉連線，下一次呼叫照常重連。
func (c *client) alive() bool {
	if c.conn == nil {
		return false
	}
	ok := false
	if sc, isRaw := c.conn.(syscall.Conn); isRaw {
		if raw, err := sc.SyscallConn(); err == nil {
			raw.Control(func(fd uintptr) {
				var b [1]byte
				_, _, err := syscall.Recvfrom(int(fd), b[:], syscall.MSG_PEEK|syscall.MSG_DONTWAIT)
				ok = err == syscall.EAGAIN || err == syscall.EWOULDBLOCK
			})
		}
	}
	if !ok {
		c.close()
	}
	return ok
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
	capsLock                         bool // 引擎記得的 Caps Lock 狀態（快照的 caps_lock，#173）
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

// ctrlPunct 回報引擎是否認得 Ctrl＋標點的鍵名而且設定開著（open 回覆的 ctrl_punctuation，#168）；快取方式同 vocabKeys。
// 舊引擎沒有這一項時為 false，Ctrl＋標點照舊交給應用程式。
func (s *session) styleShiftSpace() bool {
	if s.client.conn != nil {
		s.client.alive() // retire old connection before consulting the session cache
	}
	if !s.client.connect() || !s.ensureOpen() {
		return false
	}
	value := s.info["input_style_shift_space"]
	return value != "" && value != "disabled"
}

func (s *session) styleCapsLower() bool {
	if s.client.conn != nil {
		s.client.alive() // retire old connection before consulting the session cache
	}
	return s.client.connect() && s.ensureOpen() && s.info["input_style_caps_letter"] == "lower"
}

func (s *session) ctrlPunct(key string) bool {
	return s.client.connect() && s.ensureOpen() && s.info["ctrl_punctuation"] == "true" && ctrlPunctKnown(key, s.info)
}

// ctrlPunctKnown：引擎認得這個 Ctrl＋標點鍵名。最早的六個鍵（#168）只要有 ctrl_punctuation；Ctrl+: ( )（#231）
// 要引擎的 ctrl_punctuation_keys 列了那個字元，舊引擎沒有這一項，照舊交給應用程式。
func ctrlPunctKnown(key string, info map[string]string) bool {
	c, ok := strings.CutPrefix(key, proto.CtrlPrefix)
	if !ok || len(c) != 1 {
		return false
	}
	return strings.Contains(",.';?!", c) || strings.Contains(info["ctrl_punctuation_keys"], c)
}

// capsLockEnglish 回報引擎是否認得 caps_lock 請求而且「Caps Lock 開著時直接打英文」開著（open 回覆的 caps_lock_english，
// #173）；快取方式同 vocabKeys。舊引擎沒有這一項、或設定關閉時為 false，注音模式照舊不理 Caps Lock（#168）。
// spawn=false（Caps Lock 鍵本身）不為此啟動 0c-server：之後第一個按鍵會帶著鎖定狀態再同步一次。
// Caps Lock 鍵本身要確認快取還是新的：剛在設定程式打開這一項時引擎重啟了，open 的快取是舊的，
// 不重新確認的話第一個字母會照注音打（ㄇ）、下一個才切英文。設定改了一定會重啟引擎（舊連線跟著斷），
// 所以連線還在、session 是這一代連線開的就沿用 open 的值，不問引擎；斷了才以 health 重新確認。
func (s *session) capsLockEnglish(spawn bool) bool {
	if !spawn {
		if s.client.conn == nil {
			return false
		}
		if s.id != 0 && s.generation == s.client.generation && s.client.alive() {
			return s.info["caps_lock_english"] == "true"
		}
		reply, ok := s.client.call(ipc.Envelope{Op: ipc.OpHealth})
		if !ok { // 引擎重啟過：舊連線這一次才發現斷了，重連再問一次（同 shiftKeys）
			reply, ok = s.client.call(ipc.Envelope{Op: ipc.OpHealth})
		}
		if !ok || !reply.OK || reply.Info["caps_lock_english"] != "true" {
			return false
		}
	}
	return s.client.connect() && s.ensureOpen() && s.info["caps_lock_english"] == "true"
}

// simplifiedLive 回報引擎能即時切換「輸出簡體字」（open 回覆的 output_simplified_live，#169）。不連線，只看目前這個
// session 開啟時的回覆；還沒開 session 或連線已換過時為 false。
func (s *session) simplifiedLive() bool {
	return s.id != 0 && s.generation == s.client.generation && s.info["output_simplified_live"] == "true"
}

// ensureLive 連上引擎、開好 session 後回報 simplifiedLive；引擎不可用時 false。
func (s *session) ensureLive() bool {
	return s.client.connect() && s.ensureOpen() && s.info["output_simplified_live"] == "true"
}

// simplifiedHotkey 回報「用快捷鍵切換簡體字」開著、引擎也能即時切換（open 回覆的 simplified_hotkey 與
// output_simplified_live，#192）。只在按下那個鍵時問：設定改了一定會重啟引擎，所以先看連線還在不在（不送請求），
// 斷了就重連、重開 session 拿新的值，剛在設定程式打開或關掉後第一次按就照新設定。引擎沒在執行時照常啟動
// （這是使用者自己按的快捷鍵）；舊引擎沒有這一項時為 false，照舊交給應用程式。
func (s *session) simplifiedHotkey() bool {
	if s.client.conn != nil {
		s.client.alive() // 斷了就關掉，下面重連
	}
	return s.client.connect() && s.ensureOpen() && s.info["simplified_hotkey"] == "true" && s.info["output_simplified_live"] == "true"
}

// outputSimplified 切換「輸出簡體字」（#169）：引擎立即改變之後送出的文字並寫進設定檔，組字不動；不記進重播紀錄。
func (s *session) outputSimplified(on bool) snapshot {
	return s.handle(proto.Request{Type: proto.OutputSimplified, On: on})
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
	// 原生候選窗支援方向提示但沒有格狀版面；保留明確橫排，其他格狀設定回直排。
	if _, ok := reply.Info["candidate_layout"]; ok && reply.Info["candidate_layout"] != "horizontal" {
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
	// Caps Lock 帶來的英文（#173）：新 session 不知道 Caps Lock 開著，先還原，不然重開後的第一個字母照注音打、
	// 下一個才切回英文。設定剛關掉（引擎以新設定重啟）時不送。真正的鎖定狀態由這次按鍵帶的同步改正。
	if s.capsLock {
		if s.info["caps_lock_english"] != "true" {
			s.capsLock = false
		} else if _, ok := s.do(proto.Request{Type: proto.CapsLock, On: true}); !ok {
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
			s.english, s.englishPending, s.capsLock = snap.Mode == proto.ModeEnglish, snap.EnglishPending, snap.CapsLock
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
		s.english, s.englishPending, s.capsLock = out.english(), out.EnglishPending, out.CapsLock
		// 符號選單可以在空白組字區開啟，也必須重播才能保留選單與反白位置。
		composing := len(out.Preedit) > 0 || len(out.candidates()) > 0
		if out.Commit != "" || req.Type == proto.Cancel || !composing {
			// #90：切到英文清掉未打完的注音而結束組字時留下這段日誌；立即切回放回注音時接上，重播才得到同樣的組字。
			if req.Type == proto.Toggle && out.Commit == "" && len(s.log) > 0 {
				s.cleared = append(s.log, wire)
			}
			s.log, s.view = nil, nil
		} else if req.Type != proto.Context && req.Type != proto.CapsLock && req.Type != proto.OutputSimplified {
			// Caps Lock 狀態不重播：新 session 由下一個按鍵重新同步；「輸出簡體字」是全域設定，重播會再切一次（#169）
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
