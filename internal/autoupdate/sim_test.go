package autoupdate

// 模擬測試：以虛擬時鐘與可注入故障的假平台，跑數百個隨機情境（每個數週虛擬時間），
// 在每次喚起後檢查不變量，最後停止故障並要求收斂到最新可用版本。
// 失敗時印出種子與事件紀錄；以 -run 'TestSimulated' -sim.seed=N -v 重現單一情境。

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/update"
)

var (
	simSeeds = flag.Int("sim.seeds", 10, "模擬情境數（make test-sim 跑 300）")
	simSeed  = flag.Int64("sim.seed", 0, "只跑這個種子（重現用）")
	simDays  = flag.Int("sim.days", 10, "每個情境的混亂期（虛擬天數；make test-sim 跑 21）")
)

// crash 模擬行程在平台操作中途被殺：協調器不會存檔，下一次喚起從磁碟上的狀態繼續。
type crash struct{ op string }

func ver(n int) string { return "0.1.0-dev." + strconv.Itoa(n) }
func num(v string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(v, "0.1.0-dev."))
	if err != nil {
		return -1
	}
	return n
}
func dir(n int) string {
	if n == 0 {
		return ""
	}
	return "versions/" + ver(n) + "-c"
}
func dirNum(d string) int {
	if d == "" {
		return 0
	}
	return num(strings.TrimSuffix(strings.TrimPrefix(d, "versions/"), "-c"))
}

type world struct {
	t   *testing.T
	rng *rand.Rand
	now time.Time
	log []string

	// 伺服器
	latest     int
	serial     uint64
	rollout    int
	withdrawn  map[int]bool
	badHealth  map[int]bool
	badPrepare map[int]bool
	outage     bool
	flakyDL    float64

	// 裝置
	receipt   int // 完整安裝的版本
	pointer   int // 核心更新指標；0 為沒有
	running   int // 運作中的引擎（-1 表示健康檢查回報錯誤版本）；0 為未執行
	composing bool
	idle      time.Duration

	// 使用者本次喚起是否親自按「立即安裝」
	consent bool

	// 觀察
	seenWithdrawn      map[int]bool // 裝置最近一次成功查詢時看到的撤回清單
	switches           map[int]int
	healthy            map[int]bool // 曾以健康狀態執行的版本
	prevPointer        map[int]int  // 平台記在指標檔內的原指標
	lastNotice         map[string]time.Time
	requestedReminders map[string]time.Time // independent record of explicit user snoozes
	snoozeRequests     int
	crashProb          float64
	crashed            bool // 這次喚起中途被殺（中間狀態允許留到下一次喚起收尾）
	violations         []string
}

func (w *world) logf(format string, a ...any) {
	w.log = append(w.log, w.now.Format("01-02 15:04")+" "+fmt.Sprintf(format, a...))
}
func (w *world) violate(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	w.logf("VIOLATION %s", msg)
	w.violations = append(w.violations, w.now.Format("01-02 15:04")+" "+msg)
}
func (w *world) maybeCrash(op string) {
	if w.crashProb > 0 && w.rng.Float64() < w.crashProb {
		w.logf("crash during %s", op)
		panic(crash{op})
	}
}
func (w *world) installed() int {
	if w.pointer > 0 {
		return w.pointer
	}
	return w.receipt
}

type simPlatform struct{ w *world }

func (p simPlatform) Installed() (string, error) { return ver(p.w.installed()), nil }
func (p simPlatform) Running(context.Context) (string, bool, error) {
	switch {
	case p.w.running == 0:
		return "", false, nil
	case p.w.running < 0:
		return "broken", true, nil
	}
	// A shell may have started the new engine between coordinator wakes. This
	// call represents the real health query, so a successful observation counts
	// as verification just like StartEngine followed by Running.
	p.w.healthy[p.w.running] = true
	return ver(p.w.running), true, nil
}
func (p simPlatform) Fetch(_ context.Context, last uint64) (*update.SignedFeed, error) {
	w := p.w
	w.maybeCrash("fetch")
	if w.outage {
		return nil, errors.New("offline")
	}
	if w.serial < last {
		return nil, fmt.Errorf("serial %d older than %d", w.serial, last)
	}
	f := &update.SignedFeed{Serial: w.serial, Version: ver(w.latest), Rollout: w.rollout,
		Assets: []update.SignedAsset{{Asset: update.Asset{OS: "linux", Arch: "amd64", URL: "https://x/" + ver(w.latest),
			SHA256: strings.Repeat("a", 64), Size: 1}, Kind: "core"}}}
	w.seenWithdrawn = map[int]bool{}
	for v := range w.withdrawn {
		f.Withdrawn = append(f.Withdrawn, ver(v))
		w.seenWithdrawn[v] = true
	}
	return f, nil
}
func (p simPlatform) Download(_ context.Context, t Target, _ string) (string, error) {
	w := p.w
	w.maybeCrash("download")
	if w.outage || w.rng.Float64() < w.flakyDL {
		return "", errors.New("unexpected EOF")
	}
	return "dl/" + t.Version, nil
}
func (p simPlatform) Prepare(_ context.Context, archive string, t Target) (string, error) {
	w := p.w
	w.maybeCrash("prepare")
	if archive != "dl/"+t.Version {
		w.violate("prepare %s with archive %s", t.Version, archive)
	}
	if w.badPrepare[num(t.Version)] {
		return "", errors.New("check-data failed")
	}
	return dir(num(t.Version)), nil
}
func (p simPlatform) SwitchCore(d, version string) error {
	w := p.w
	w.maybeCrash("switch-before")
	n := num(version)
	if dirNum(d) != n {
		w.violate("switch dir %s for %s", d, version)
	}
	if w.pointer == n {
		return nil // 已切換（前一次中途被中止）；平台保留最初的原指標
	}
	if w.seenWithdrawn[n] {
		w.violate("switched to %s which the device already knew was withdrawn", version)
	}
	if !w.consent && w.idle < 5*time.Minute {
		w.violate("switched to %s while user active (idle %s) without consent", version, w.idle)
	}
	if n <= w.installed() {
		w.violate("downgrade/sidegrade switch %d -> %s", w.installed(), version)
	}
	w.switches[n]++
	if w.badHealth[n] && w.switches[n] > 1 {
		w.violate("switched again to %s after it failed health check", version)
	}
	w.prevPointer[n] = w.pointer
	w.logf("switch pointer %d -> %d", w.pointer, n)
	w.pointer = n
	w.maybeCrash("switch-after")
	return nil
}
func (p simPlatform) RevertCore(d string) error {
	w := p.w
	w.maybeCrash("revert")
	n := dirNum(d)
	if w.pointer != n {
		return nil
	}
	w.pointer = w.prevPointer[n]
	w.logf("revert pointer %d -> %d", n, w.pointer)
	return nil
}
func (p simPlatform) Drain(context.Context) error {
	w := p.w
	w.maybeCrash("drain")
	if w.running != 0 && w.composing {
		return errors.New("composing")
	}
	w.running = 0
	return nil
}
func (p simPlatform) StartEngine(context.Context) error {
	w := p.w
	w.maybeCrash("start")
	if w.running != 0 {
		if w.composing {
			w.violate("started a new engine while the running one was composing")
		}
		return nil // 已有引擎佔用管道；新行程無法取得，維持原引擎
	}
	v := w.installed()
	if w.badHealth[v] {
		w.running = -1
	} else {
		w.running = v
		w.healthy[v] = true
	}
	w.logf("engine started: %d", w.running)
	return nil
}
func (p simPlatform) InstallShell(context.Context, string) (bool, error) {
	p.w.violate("shell install requested in a core-only simulation")
	return false, errors.New("unexpected")
}
func (p simPlatform) Conditions() Conditions {
	return Conditions{Idle: p.w.idle, IdleKnown: true}
}
func (p simPlatform) Notify(n Notice) error {
	w := p.w
	key := n.Title + "|" + n.Body
	if n.TargetVersion != "" {
		// A change of wording/reason is not a new version. Track independently
		// of the coordinator's reminder count and blocker bookkeeping.
		key = n.TargetVersion
	}
	asset := n.TargetVersion + "/" + n.TargetSHA256
	until, requested := w.requestedReminders[asset]
	if requested && w.now.Before(until) {
		w.violate("requested reminder before its deadline: %s", asset)
	}
	requestedDue := requested && !w.now.Before(until)
	if last, ok := w.lastNotice[key]; ok && w.now.Sub(last) < 24*time.Hour && !requestedDue {
		w.violate("same notice within 24h: %s", key)
	}
	if requestedDue {
		delete(w.requestedReminders, asset)
	}
	w.lastNotice[key] = w.now
	w.logf("notify: %s", n.Body)
	return nil
}
func (p simPlatform) Target() (string, string) { return "linux", "amd64" }

func (w *world) wake(base string, force bool) *State {
	co := &Coordinator{Base: base, Platform: simPlatform{w}, Now: func() time.Time { return w.now },
		Jitter: func(max time.Duration) time.Duration { return time.Duration(w.rng.Int63n(int64(max) + 1)) },
		Force:  force, Consent: w.consent}
	var s *State
	w.crashed = false
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(crash); !ok {
					panic(r)
				}
				w.crashed = true
			}
		}()
		var err error
		if s, err = co.Run(context.Background()); err != nil {
			w.violate("run error: %v", err)
		}
	}()
	if s == nil {
		s, _ = Load(base)
	}
	return s
}

func (w *world) check(s *State) {
	if w.crashed {
		return // 被殺的喚起可以留下中間狀態；下一次沒被殺的喚起必須收尾（由下面的檢查把關）
	}
	// 指標只能指向目標版本（切換中），或確認健康執行過的版本；不能停在未經檢查的其他版本。
	if w.pointer != 0 && !w.healthy[w.pointer] && (!s.Switched || s.Target == nil || w.pointer != num(s.Target.Version)) {
		w.violate("pointer on unverified version %d (switched %v target %+v)", w.pointer, s.Switched, s.Target)
	}
	if s.Switched && s.Target == nil {
		w.violate("state says switched without a target")
	}
	if w.running > 0 && w.running != w.installed() && s.Stage != StagePending && !s.Switched && !s.RestartPending {
		w.violate("engine %d running but installed is %d (stage %s)", w.running, w.installed(), s.Stage)
	}
	if w.running < 0 && !s.Switched && !s.RestartPending {
		w.violate("broken engine left running after the wake (stage %s)", s.Stage)
	}
	for _, v := range s.Failed {
		if w.pointer == num(v) && !s.Switched {
			w.violate("pointer on failed version %s", v)
		}
	}
}

func TestSimulationRecognizesHealthOfExternallyStartedEngine(t *testing.T) {
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	w := &world{t: t, now: now, receipt: 18, pointer: 19, running: 19, healthy: map[int]bool{18: true}}
	base := t.TempDir()
	s, err := Load(base)
	if err != nil {
		t.Fatal(err)
	}
	s.Stage, s.Switched, s.Prepared = StagePending, true, dir(19)
	s.Target = &Target{Version: ver(19), Kind: "core"}
	s.LastCheck, s.NextCheck = now, now.Add(6*time.Hour)
	if err := Save(base, s); err != nil {
		t.Fatal(err)
	}
	co := &Coordinator{Base: base, Platform: simPlatform{w}, Now: func() time.Time { return now }}
	s, err = co.Run(context.Background())
	if err != nil || s.Stage != StageActive || !w.healthy[19] {
		t.Fatalf("external launch health was not recognized: state=%+v err=%v", s, err)
	}
	w.check(s)
	if len(w.violations) != 0 {
		t.Fatal(w.violations)
	}
}

func TestSimulationDistinguishesTrackedRollbackFromUntrackedMismatch(t *testing.T) {
	w := &world{t: t, receipt: 18, pointer: 18, running: 19, composing: true, healthy: map[int]bool{18: true}}
	s := &State{Stage: StageDownloaded, RestartPending: true, Target: &Target{Version: ver(20), Kind: "core"}}
	w.check(s)
	if len(w.violations) != 0 {
		t.Fatal("known rollback may wait for old composition", w.violations)
	}
	s.RestartPending = false
	w.check(s)
	if len(w.violations) != 1 {
		t.Fatal("untracked running/pointer mismatch must still fail")
	}
	w.violations = nil
	s.RestartPending, w.pointer = true, 19
	w.check(s)
	if len(w.violations) == 0 {
		t.Fatal("restart pending must not excuse an unverified pointer")
	}
}

func simulate(t *testing.T, seed int64, days int) (w *world) {
	w = &world{t: t, rng: rand.New(rand.NewSource(seed)), now: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		latest: 18, serial: 1, rollout: 100, withdrawn: map[int]bool{}, badHealth: map[int]bool{}, badPrepare: map[int]bool{},
		receipt: 18, running: 18, flakyDL: 0.15, switches: map[int]int{}, healthy: map[int]bool{18: true}, prevPointer: map[int]int{}, lastNotice: map[string]time.Time{},
		crashProb: 0.03, idle: time.Hour, requestedReminders: map[string]time.Time{}}
	base := t.TempDir()
	// 分批資格依安裝識別碼決定；固定由種子產生，情境才能重現。
	if st, err := Load(base); err == nil {
		st.InstallID = fmt.Sprintf("sim-%d", seed)
		Save(base, st)
	}
	typing := 0
	tick := 15 * time.Minute
	chaos := func(steps int) {
		for i := 0; i < steps; i++ {
			r := w.rng
			w.now = w.now.Add(tick)
			// 使用者：打字一段時間（期間可能停在組字中），之後閒置。
			if typing > 0 {
				typing--
				w.idle, w.composing = 0, r.Float64() < 0.4
			} else {
				w.idle += tick
				w.composing = r.Float64() < 0.05 // 閒置時也可能把半句留在組字區
				if r.Float64() < 0.08 {
					typing = 1 + r.Intn(16)
				}
			}
			// 伺服器事件
			switch x := r.Float64(); {
			case x < 0.012:
				w.latest++
				w.serial++
				w.badHealth[w.latest] = r.Float64() < 0.15
				w.badPrepare[w.latest] = r.Float64() < 0.05
				w.rollout = []int{1, 10, 50, 100, 100}[r.Intn(5)]
				w.logf("publish %d (bad health %v, bad prepare %v, rollout %d)", w.latest, w.badHealth[w.latest], w.badPrepare[w.latest], w.rollout)
			case x < 0.017:
				w.withdrawn[w.latest] = true
				w.serial++
				w.logf("withdraw %d", w.latest)
			case x < 0.03:
				w.serial++ // 續簽
			case x < 0.05:
				w.outage = !w.outage
				w.logf("outage %v", w.outage)
			case x < 0.052:
				w.rollout = 100
				w.serial++
			}
			// 時鐘跳動
			if r.Float64() < 0.002 {
				j := time.Duration(r.Intn(480)-240) * time.Hour
				w.now = w.now.Add(j)
				w.logf("clock jump %s", j)
			}
			// 使用者操作
			force := false
			w.consent = false
			switch x := r.Float64(); {
			case x < 0.004:
				w.consent, force = true, true
				w.logf("user: finish now")
			case x < 0.006:
				s, _ := Load(base)
				if s.Mode == ModeAuto {
					s.Mode = ModeAsk
				} else {
					s.Mode = ModeAuto
				}
				Save(base, s)
				w.logf("user: mode %s", s.Mode)
			case x < 0.007:
				s, _ := Load(base)
				s.PausedUntil = w.now.Add(24 * time.Hour)
				Save(base, s)
				w.logf("user: pause 1 day")
			case x < 0.009:
				s, _ := Load(base)
				if s.Target != nil && s.Stage == StagePrepared && !s.Switched && !s.failed(s.Target.Version) {
					if err := SnoozeNotice(base, s.Target.Version, s.Target.SHA256, w.now); err != nil {
						w.violate("snooze request failed: %v", err)
					}
					w.requestedReminders[s.Target.Version+"/"+s.Target.SHA256] = w.now.Add(time.Hour)
					w.snoozeRequests++
					w.logf("user: snooze %s for 1h", s.Target.Version)
				}
			}
			// 引擎閒置退出（-idle-exit）與下一個按鍵啟動
			if w.running > 0 && !w.composing && w.idle > 5*time.Hour {
				w.running = 0
			}
			if typing > 0 && w.running == 0 {
				v := w.installed()
				if w.badHealth[v] {
					w.running = -1
				} else {
					w.running = v
				}
			}
			s := w.wake(base, force)
			w.check(s)
		}
	}
	chaos(days * 96)

	// 收斂：停止所有故障，發布一個好的新版，使用者多數時間閒置、偶爾打字；自動模式、沒有暫停。
	w.crashProb, w.flakyDL, w.outage = 0, 0, false
	w.latest++
	w.serial++
	w.rollout = 100
	w.badHealth[w.latest], w.badPrepare[w.latest] = false, false
	w.logf("final publish %d", w.latest)
	s, _ := Load(base)
	s.Mode, s.PausedUntil = ModeAuto, time.Time{}
	Save(base, s)
	for i := 0; i < 3*96; i++ {
		w.now = w.now.Add(tick)
		w.consent = false
		w.idle += tick
		w.composing = false
		if i%40 == 0 { // 偶爾打一段字
			w.idle = 0
			if w.running == 0 {
				w.running = w.installed()
			}
		}
		s = w.wake(base, false)
		w.check(s)
	}
	if w.installed() != w.latest || (w.running != 0 && w.running != w.latest) || s.Target != nil || s.Switched || s.RestartPending {
		w.violate("did not converge in 3 days: installed %d running %d latest %d stage %s blocked %+v target %+v next check %s",
			w.installed(), w.running, w.latest, s.Stage, s.Blocked, s.Target, s.NextCheck.Format("01-02 15:04"))
	}
	return w
}

func TestSimulatedCoreUpdates(t *testing.T) {
	if testing.Short() {
		t.Skip("模擬測試較久；-short 時略過")
	}
	seeds := []int64{}
	if *simSeed != 0 {
		seeds = append(seeds, *simSeed)
	} else {
		for i := 1; i <= *simSeeds; i++ {
			seeds = append(seeds, int64(i))
		}
	}
	failed := 0
	requested := 0
	for _, seed := range seeds {
		w := simulate(t, seed, *simDays)
		requested += w.snoozeRequests
		if len(w.violations) == 0 {
			continue
		}
		failed++
		if failed <= 5 {
			t.Errorf("seed %d：%d 個違反，第一個：%s", seed, len(w.violations), w.violations[0])
			if *simSeed != 0 || failed == 1 {
				end := len(w.log)
				for i, l := range w.log {
					if strings.Contains(l, "VIOLATION") {
						end = i + 1
						break
					}
				}
				t.Logf("seed %d 第一次違反前的事件：\n%s", seed, strings.Join(w.log[max(0, end-40):end], "\n"))
			}
		}
	}
	if failed > 0 {
		t.Errorf("%d／%d 個情境違反不變量", failed, len(seeds))
	}
	// The quick 10×10 suite can legitimately miss rare user actions. Require
	// snooze coverage in the full acceptance matrix; boundary tests always run.
	if *simSeed == 0 && len(seeds) >= 300 && *simDays >= 21 && requested == 0 {
		t.Error("random suite did not exercise any explicit snooze request")
	}
	t.Logf("explicit snooze requests exercised: %d", requested)
}

func TestSimulationSnoozeObserverAllowsOnlyOneRequestedDueReminder(t *testing.T) {
	w := &world{now: time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC), lastNotice: map[string]time.Time{}, requestedReminders: map[string]time.Time{}}
	n := Notice{Title: "人工通知", Body: "人工更新", Action: "finish", TargetVersion: ver(41), TargetSHA256: strings.Repeat("a", 64)}
	p := simPlatform{w}
	p.Notify(n)
	w.now = w.now.Add(time.Hour)
	w.requestedReminders[n.TargetVersion+"/"+n.TargetSHA256] = w.now
	p.Notify(n)
	if len(w.violations) != 0 {
		t.Fatal("explicit due reminder was mistaken for spam", w.violations)
	}
	p.Notify(n)
	if len(w.violations) != 1 {
		t.Fatal("a second unrequested repeat must remain a violation")
	}
	w.violations = nil
	w.requestedReminders[n.TargetVersion+"/"+n.TargetSHA256] = w.now.Add(time.Hour)
	p.Notify(n)
	if len(w.violations) == 0 {
		t.Fatal("early reminders must remain a violation")
	}
}
