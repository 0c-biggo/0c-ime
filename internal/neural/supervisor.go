package neural

import (
	"sync"
	"time"

	"github.com/0c-biggo/0c-ime/core/rerank"
)

// Supervisor 在 worker 出錯（含逾時）後於背景重啟，按鍵永遠不等待啟動。
// 出錯的那一次與重啟完成前的請求都回 ErrUnavailable，session 保留統計引擎結果；
// 每次重啟都重新核對套件雜湊與字典，冷卻與每小時上限避免反覆崩潰佔用 CPU。
type Supervisor struct {
	mu          sync.Mutex
	cfg         Config
	open        func(Config) (*Worker, error)
	current     *Worker
	manifest    Manifest
	starting    bool
	closed      bool
	lastFailure time.Time
	reason      string // 最近一次啟動失敗的原因代碼；成功啟動後清空
	window      time.Time
	restarts    int
	total       Stats
	restarted   int
	Cooldown    time.Duration
	MaxPerHour  int
	Log         func(string, ...any)
}

func OpenSupervised(c Config) (*Supervisor, error) {
	return openSupervised(c, Open)
}

func openSupervised(c Config, open func(Config) (*Worker, error)) (*Supervisor, error) {
	w, err := open(c)
	if err != nil {
		return nil, err
	}
	return &Supervisor{cfg: c, open: open, current: w, manifest: w.manifest, Cooldown: 3 * time.Second, MaxPerHour: 30}, nil
}

// OpenSupervisedAsync 同步核對套件與字典配對，第一個 worker 在背景啟動：
// 引擎不必等 Python 載入模型才開始服務，就緒前的按鍵保留統計引擎結果。
func OpenSupervisedAsync(c Config) (*Supervisor, error) {
	return openSupervisedAsync(c, loadManifest, Open)
}

func openSupervisedAsync(c Config, load func(Config) (Manifest, error), open func(Config) (*Worker, error)) (*Supervisor, error) {
	m, err := load(c)
	if err != nil {
		return nil, err
	}
	s := &Supervisor{cfg: c, open: open, manifest: m, Cooldown: 3 * time.Second, MaxPerHour: 30}
	s.mu.Lock()
	s.launch(false)
	s.mu.Unlock()
	return s, nil
}

func (s *Supervisor) Options() rerank.Options {
	return rerank.Options{Scorer: s, Weight: s.manifest.Weight, Margin: s.manifest.Margin, AllowPartial: s.manifest.AllowPartial,
		ContextRunes: contextRunes(s.manifest), HintGap: s.manifest.HintGap,
		TextWeight: s.manifest.TextWeight, TextMargin: s.manifest.TextMargin,
		NeutralExpand: s.manifest.NeutralExpand, NeutralBases: neutralBases(s.manifest)}
}

func (s *Supervisor) Score(r rerank.Request) ([]float64, error) {
	s.mu.Lock()
	w := s.current
	if w == nil {
		s.total.Calls++
		s.total.Unavailable++
		s.maybeRestart()
		s.mu.Unlock()
		return nil, ErrUnavailable
	}
	s.mu.Unlock()
	scores, err := w.Score(r)
	if err != nil && w.Dead() {
		s.mu.Lock()
		if s.current == w {
			s.retire(w)
			s.lastFailure = time.Now()
			s.maybeRestart()
		}
		s.mu.Unlock()
	}
	return scores, err
}

// retire 需持有 s.mu：累計舊 worker 的統計並在背景回收行程。
func (s *Supervisor) retire(w *Worker) {
	s.accumulate(w)
	s.current = nil
	go w.Close()
}

// accumulate 需持有 s.mu。
func (s *Supervisor) accumulate(w *Worker) {
	st := w.Stats()
	s.total.Calls += st.Calls
	s.total.Scored += st.Scored
	s.total.Timeouts += st.Timeouts
	s.total.Errors += st.Errors
	s.total.Unavailable += st.Unavailable
	s.total.TotalUS += st.TotalUS
}

// maybeRestart 需持有 s.mu。
func (s *Supervisor) maybeRestart() {
	now := time.Now()
	if s.closed || s.starting || now.Sub(s.lastFailure) < retryCooldown(s.reason, s.Cooldown) {
		return
	}
	if now.Sub(s.window) >= time.Hour {
		s.window, s.restarts = now, 0
	}
	if s.restarts >= s.MaxPerHour {
		return
	}
	s.restarts++
	s.launch(true)
}

// launch 需持有 s.mu：在背景啟動 worker，就緒後才接手評分。
func (s *Supervisor) launch(restart bool) {
	s.starting = true
	go func() {
		w, err := s.open(s.cfg)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.starting = false
		if err != nil {
			s.lastFailure = time.Now()
			s.reason = startupReason(err)
			if s.Log != nil {
				s.Log("神經 worker 啟動失敗，保留統計引擎：%v", err)
			}
			return
		}
		if s.closed {
			go w.Close()
			return
		}
		s.current = w
		s.reason = ""
		if !restart {
			return
		}
		s.restarted++
		if s.Log != nil {
			s.Log("神經 worker 已重啟（第 %d 次）", s.restarted)
		}
	}()
}

func (s *Supervisor) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.total
	if s.current != nil {
		c := s.current.Stats()
		st.Calls += c.Calls
		st.Scored += c.Scored
		st.Timeouts += c.Timeouts
		st.Errors += c.Errors
		st.Unavailable += c.Unavailable
		st.TotalUS += c.TotalUS
	}
	return st
}

// SetLog 設定紀錄函式；背景啟動可能同時讀取，須經由鎖設定。
func (s *Supervisor) SetLog(f func(string, ...any)) { s.mu.Lock(); s.Log = f; s.mu.Unlock() }

// State 回報當下模型可用性；查詢不觸發重啟或評分，也不含輸入文字。
func (s *Supervisor) State() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "closed"
	}
	if s.current != nil && !s.current.Dead() {
		select {
		case <-s.current.done:
			return "fallback"
		default:
			return "ready"
		}
	}
	if s.starting {
		return "loading"
	}
	if s.reason == ReasonRuntimeMissing {
		return "runtime_missing"
	}
	return "fallback"
}

// Reason 回傳最近一次啟動失敗的原因代碼（StartupError.Reason）；沒有失敗或已恢復時為空字串。
func (s *Supervisor) Reason() string { s.mu.Lock(); defer s.mu.Unlock(); return s.reason }

// Restarts 回傳成功重啟的次數。
func (s *Supervisor) Restarts() int { s.mu.Lock(); defer s.mu.Unlock(); return s.restarted }

func (s *Supervisor) Close() {
	s.mu.Lock()
	s.closed = true
	w := s.current
	if w != nil {
		s.accumulate(w) // 關閉後的 Stats 仍含目前 worker 的累計
	}
	s.current = nil
	s.mu.Unlock()
	if w != nil {
		w.Close()
	}
}
