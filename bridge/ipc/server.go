package ipc

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/core/proto"
)

// Server 在監聽器上為每個連線提供引擎服務。
type Server struct {
	Engine *engine.Engine
	// OnCommit 在提交、明確刪除學習或更新表情排序後呼叫（例如排程存檔）。
	OnCommit func()
	// OnForget 在明確刪除學習後、OnCommit 之後呼叫：使用者要求刪除的內容應盡快從磁碟上消失，
	// 宿主不必等停止打字才存檔。
	OnForget func()
	Log      *log.Logger
	Info     map[string]string
	// RuntimeInfo 提供會改變的運作狀態；須可並行查詢且不能包含個人文字。
	RuntimeInfo func() map[string]string
	// OnDrained 在交接條件成立、BeforeDrain 成功並斷開所有連線後呼叫；宿主應結束服務。
	OnDrained func()
	// BeforeDrain runs after active requests finish, while endpoints still belong
	// to this writer. On error the server resumes and retains its unsaved state.
	BeforeDrain func() error
	// DrainTimeout 是等待組字結束的上限；逾時放棄交接、繼續服務。
	DrainTimeout time.Duration
	// VocabDisabled 不為空時拒絕自訂詞彙指令並回報原因（詞彙檔由較新版本寫成或讀不到、另一個引擎持有個人資料鎖）。
	VocabDisabled string
	// OnVocab 在自訂詞彙指令改了詞彙後呼叫；宿主應立即存檔。
	OnVocab func()

	mu              sync.Mutex
	conns           map[net.Conn]bool
	draining        bool
	settingsPending bool
	lastKey         time.Time // 最近一次按鍵請求；更新協調器在 OS 無法判斷閒置時以此判斷
	closing         bool
	active          int        // 處理中的請求；交接須等它們完成後才存檔
	idle            *sync.Cond // active 歸零時通知（與 mu 配對）
}

// Serve 接受連線直到監聽器關閉。可對多個監聽器同時呼叫（例如過渡期的新舊名稱），共用引擎與連線計數。
func (s *Server) Serve(l net.Listener) error {
	s.mu.Lock()
	if s.conns == nil {
		s.conns = map[net.Conn]bool{}
	}
	if s.lastKey.IsZero() {
		s.lastKey = time.Now() // 啟動後尚未有按鍵：從開始服務起算
	}
	s.mu.Unlock()
	for {
		c, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		s.mu.Lock()
		s.conns[c] = true
		s.mu.Unlock()
		go s.handle(c)
	}
}

func (s *Server) info() map[string]string {
	info := map[string]string{}
	for k, v := range s.Info {
		info[k] = v
	}
	if s.RuntimeInfo != nil {
		for k, v := range s.RuntimeInfo() {
			info[k] = v
		}
	}
	return info
}

func (s *Server) health() map[string]string {
	info := s.info()
	total, composing := s.Engine.Sessions()
	s.mu.Lock()
	info["draining"] = fmt.Sprint(s.draining)
	info["settings_deferred_apply"] = "true"
	info["connections"] = fmt.Sprint(len(s.conns))
	if !s.lastKey.IsZero() {
		info["idle_seconds"] = fmt.Sprint(int(time.Since(s.lastKey).Seconds()))
	}
	s.mu.Unlock()
	info["sessions"] = fmt.Sprint(total)
	info["composing"] = fmt.Sprint(composing)
	info["pid"] = fmt.Sprint(os.Getpid())
	return info
}

// LastKey 回傳最近一次按鍵請求的時間（尚未服務時為零值）；宿主據此判斷使用者是否停止打字。
func (s *Server) LastKey() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastKey
}

// markDrain 標記交接開始（health 立即回報 draining）；回傳是否為新的交接，由 handle 在回覆寫出後啟動。
func (s *Server) markDrain() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.draining {
		return false
	}
	s.draining = true
	return true
}

// Drain 由宿主自己開始交接（例如系統安裝的引擎發現執行檔或字典被套件管理員換掉，#181），
// 與收到 drain 請求相同：沒有組字時存檔、斷開連線並呼叫 OnDrained；逾時放棄。已在交接中時回傳 false。
func (s *Server) Drain() bool {
	if !s.markDrain() {
		return false
	}
	s.runDrain()
	return true
}

// Draining 回報交接是否進行中或已完成；逾時或存檔失敗而放棄交接後回到 false。
func (s *Server) Draining() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.draining
}

// runDrain 在背景等待所有組字結束；成立時斷開連線並通知宿主退出，逾時則放棄交接。
func (s *Server) runDrain() {
	timeout := s.DrainTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	go func() {
		deadline := time.Now().Add(timeout)
		for {
			s.mu.Lock()
			pending := s.settingsPending
			s.mu.Unlock()
			if !pending && !time.Now().Before(deadline) {
				break
			}
			if _, composing := s.Engine.Sessions(); composing == 0 {
				s.mu.Lock()
				if s.idle == nil {
					s.idle = sync.NewCond(&s.mu)
				}
				for s.active > 0 { // 已開始處理的請求（可能含提交）完成後才讓宿主存檔
					s.idle.Wait()
				}
				// A request may have started a composition after the initial
				// check. Holding mu prevents new handlers entering until the
				// final check and transition to closing are complete.
				if _, composing := s.Engine.Sessions(); composing != 0 {
					s.mu.Unlock()
					time.Sleep(50 * time.Millisecond)
					continue
				}
				s.closing = true
				for c := range s.conns {
					c.Close()
				}
				s.mu.Unlock()
				if s.BeforeDrain != nil {
					if err := s.BeforeDrain(); err != nil {
						s.mu.Lock()
						s.closing, s.draining = false, false
						s.mu.Unlock()
						if s.Log != nil {
							s.Log.Printf("交接存檔失敗，繼續服務：%v", err)
						}
						return
					}
				}
				if s.Log != nil {
					s.Log.Printf("交接：沒有組字，存檔後退出")
				}
				if s.OnDrained != nil {
					s.OnDrained()
				}
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		s.mu.Lock()
		s.draining = false
		s.mu.Unlock()
		if s.Log != nil {
			s.Log.Printf("交接逾時：仍有組字，繼續服務")
		}
	}()
}

// Conns 回傳目前的連線數。
func (s *Server) Conns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

func (s *Server) handle(c net.Conn) {
	sessions := map[uint64]bool{}
	defer func() {
		for id := range sessions {
			s.Engine.CloseSession(id)
		}
		c.Close()
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}()
	for {
		var env Envelope
		if err := ReadFrame(c, &env); err != nil {
			if !errors.Is(err, io.EOF) && s.Log != nil {
				s.Log.Printf("連線錯誤：%v", err)
			}
			return
		}
		s.mu.Lock()
		s.active++
		s.mu.Unlock()
		startDrain := false
		reply := s.do(env, sessions, &startDrain)
		writeErr := WriteFrame(c, reply)
		s.mu.Lock()
		if s.active--; s.active == 0 && s.idle != nil {
			s.idle.Broadcast()
		}
		s.mu.Unlock()
		if writeErr != nil {
			return
		}
		if startDrain {
			// 回覆寫出後才開始交接：沒有組字時交接會立刻關閉所有連線，包括這一條。
			s.runDrain()
		}
	}
}

func (s *Server) do(env Envelope, sessions map[uint64]bool, startDrain *bool) Reply {
	s.mu.Lock()
	closing := s.closing
	s.mu.Unlock()
	if closing {
		// 交接中：外殼視為連線失敗，重連時接上新版引擎並重播組字。
		return Reply{Error: "引擎正在切換"}
	}
	switch env.Op {
	case OpPing:
		return Reply{OK: true}
	case OpHealth:
		return Reply{OK: true, Info: s.health()}
	case OpApplySettings:
		s.mu.Lock()
		s.settingsPending = true
		s.mu.Unlock()
		*startDrain = s.markDrain()
		return Reply{OK: true, Info: s.health()}
	case OpDrain:
		*startDrain = s.markDrain()
		return Reply{OK: true, Info: s.health()} // 交接在回覆寫出後才開始（handle）
	case OpOpen:
		if env.Version != proto.Version {
			return Reply{Error: "協定版本不符"}
		}
		id := s.Engine.OpenSession()
		sessions[id] = true
		return Reply{OK: true, Session: id, Info: s.info()}
	case OpClose:
		if sessions[env.Session] {
			s.Engine.CloseSession(env.Session)
			delete(sessions, env.Session)
		}
		return Reply{OK: true}
	case OpHandle:
		if !sessions[env.Session] {
			return Reply{Error: "session 不存在或不屬於這個連線"}
		}
		if env.Req == nil {
			return Reply{Error: "缺少請求"}
		}
		started := time.Now()
		s.mu.Lock()
		s.lastKey = started
		s.mu.Unlock()
		snap, err := s.Engine.Do(env.Session, *env.Req)
		engineUS := time.Since(started).Microseconds()
		if err != nil {
			return Reply{Error: err.Error()}
		}
		if (snap.Commit != "" || snap.PersonalChanged || env.Req.Type == proto.Forget) && s.OnCommit != nil {
			s.OnCommit()
		}
		if (env.Req.Type == proto.Forget || snap.Forgot) && s.OnForget != nil {
			s.OnForget()
		}
		if snap.VocabChanged && s.OnVocab != nil { // 組字中加詞或反悔鍵（#123）
			s.OnVocab()
		}
		return Reply{OK: true, Snapshot: &snap, EngineUS: engineUS}
	case OpVocab:
		if s.VocabDisabled != "" {
			return Reply{Error: s.VocabDisabled}
		}
		if env.Vocab == nil {
			return Reply{Error: "缺少詞彙指令"}
		}
		resp := s.Engine.Vocab(*env.Vocab)
		if resp.Changed && engine.LearningOp(env.Vocab.Op) { // 改的是學習資料（不再學、移除、恢復學習、復原）：照輸入法的忘記處理
			if s.OnCommit != nil {
				s.OnCommit()
			}
			if s.OnForget != nil {
				s.OnForget()
			}
		} else if resp.Changed && s.OnVocab != nil {
			s.OnVocab()
		}
		return Reply{OK: true, Vocab: &resp}
	}
	return Reply{Error: "不認得的操作 " + env.Op}
}
