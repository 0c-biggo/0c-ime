package session

import (
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func autoConfig(pairs ...fuzzy.Pair) fuzzy.Config {
	var c fuzzy.Config
	for _, p := range pairs {
		c = c.With(p, fuzzy.Auto)
	}
	return c
}

func autoSession(cfg fuzzy.Config, typed string, extra ...word) *Session {
	s := fuzzySession(cfg, append(fillers(typed), extra...)...)
	s.Opt.PrefixAware = false
	return s
}

// 精確讀音沒有像樣的詞、近音版本是常見詞時自動改字；改的字以「已修正」樣式標示。
func TestFuzzyAutoFixesWhenTheExactReadingHasNoWord(t *testing.T) {
	const typed = "ㄘ ㄈㄢˋ"
	s := autoSession(autoConfig(fuzzy.CCh), typed)
	snap := fzType(s, typed)
	if snap.Text() != "吃飯" {
		t.Fatalf("顯示 %q，應自動改成吃飯", snap.Text())
	}
	var corrected string
	for _, seg := range snap.Preedit {
		if seg.Style == proto.StyleCorrected {
			corrected += seg.Text
		}
	}
	if corrected == "" {
		t.Errorf("自動修正的字要標示：%+v", snap.Preedit)
	}
	// 控制組：同樣的詞庫只開「僅供選字」，顯示不變。
	sel := fuzzySession(fuzzy.Config{}.With(fuzzy.CCh, fuzzy.Select), fillers(typed)...)
	sel.Opt.PrefixAware = false
	if got := fzType(sel, typed).Text(); got == "吃飯" {
		t.Errorf("僅供選字不得自動改字：%q", got)
	}
}

// 原音首選有語境支持時不改：近音字的先驗比較高也一樣（單音節要更高的門檻）。
func TestFuzzyAutoKeepsAnExactChoiceTheContextSupports(t *testing.T) {
	typed := "ㄧㄣ"
	extra := []word{{"嬴", "ㄧㄥ", 1e-2}}
	// 原音首選「因」先驗 8e-4、近音「嬴」1e-2：含通道成本後近音只領先約 1.3，遠低於單音節門檻 7。
	lex := func() *Session {
		s := fuzzySession(autoConfig(fuzzy.EnEng), append(fillers(typed), extra...)...)
		s.Opt.PrefixAware = false
		return s
	}
	got := fzType(lex(), typed).Text()
	if got == "嬴" {
		t.Fatalf("單音節的小幅領先不得自動改字：%q", got)
	}
	// 突變：門檻歸零就會改——證明保護來自門檻，而不是別的路徑。
	s := lex()
	s.Opt.FuzzyAuto = fuzzy.AutoPolicy{}
	if got := fzType(s, typed).Text(); got != "嬴" {
		t.Fatalf("門檻歸零時應改成領先者：%q", got)
	}
}

// 一處有把握、另一處沒把握時，只改前者（每處各自比較）。
func TestFuzzyAutoJudgesEachChangeOnItsOwn(t *testing.T) {
	typed := "ㄘ ㄈㄢˋ ㄧㄣ"
	extra := []word{{"嬴", "ㄧㄥ", 1e-2}}
	s := autoSession(autoConfig(fuzzy.CCh, fuzzy.EnEng), typed, extra...)
	got := fzType(s, typed).Text()
	if got != "吃飯因" {
		t.Fatalf("顯示 %q：吃飯 有把握要改，ㄧㄣ 只是小幅領先不改", got)
	}
}

// 只對設為 auto 的配對自動修正；其他配對（select）仍只供選字。
func TestFuzzyAutoOnlyAppliesToAutoPairs(t *testing.T) {
	const typed = "ㄘ ㄈㄢˋ"
	cfg := fuzzy.Config{}.With(fuzzy.CCh, fuzzy.Select).With(fuzzy.ZZh, fuzzy.Auto)
	s := autoSession(cfg, typed)
	if got := fzType(s, typed).Text(); got == "吃飯" {
		t.Fatalf("select 配對不得自動改字：%q", got)
	}
	// 但 select 的行為仍在：近音候選照常出現。
	if _, ok := find(listCandidates(s, 0, 2), "吃飯"); !ok {
		t.Error("auto 模式的候選視窗仍須合併近音（含 select 配對）")
	}
}

// F31：手選原音推翻自動修正，推翻後鎖定，繼續輸入也不被改回。
func TestFuzzyAutoCanBeOverriddenByAnExactPick(t *testing.T) {
	const typed = "ㄘ ㄈㄢˋ"
	s := autoSession(autoConfig(fuzzy.CCh), typed)
	if fzType(s, typed).Text() != "吃飯" {
		t.Fatal("前提：先自動改成吃飯")
	}
	items := listCandidates(s, 0, 1)
	var exact string
	for _, it := range items {
		if it.Note == "" && it.Text != "" && zhuyin.Syllable("ㄘ").Legal() {
			exact = it.Text
			break
		}
	}
	if exact == "" {
		t.Fatalf("候選沒有原音字：%v", texts(items))
	}
	fzSelect(t, s, 0, 1, exact)
	if got := s.Handle(proto.Request{Type: proto.Key, Key: "z"}).Text(); strings.Contains(got, "吃") {
		t.Fatalf("手選原音後不得再被自動改回近音：%q", got)
	}
	snap := s.Handle(proto.Request{Type: proto.Commit})
	if strings.Contains(snap.Commit, "吃") {
		t.Fatalf("提交 %q", snap.Commit)
	}
}

// 刪除再輸入、關閉設定：自動修正只是顯示，不留下任何狀態。
func TestFuzzyAutoLeavesNoLockAndFollowsEditing(t *testing.T) {
	const typed = "ㄘ ㄈㄢˋ"
	s := autoSession(autoConfig(fuzzy.CCh), typed)
	fzType(s, typed)
	if len(s.locks) != 0 {
		t.Fatalf("自動修正不得建立鎖定：%v", s.locks)
	}
	s.Handle(proto.Request{Type: proto.Key, Key: "Backspace"})
	if got := s.snapshot().Text(); got == "吃飯" || got == "吃" {
		t.Fatalf("刪掉音節後顯示應重新解碼：%q", got)
	}
	if got := fzType(s, "ㄈㄢˋ").Text(); got != "吃飯" {
		t.Fatalf("重新輸入後再次自動修正：%q", got)
	}
}

// 自動修正的字不進學習：不記讀音、單字手選、片語、詞頻，也不當作拒絕。
func TestFuzzyAutoIsNotLearned(t *testing.T) {
	const typed = "ㄨㄛˇ ㄘ ㄈㄢˋ"
	s := autoSession(autoConfig(fuzzy.CCh), typed)
	if got := fzType(s, typed).Text(); got != "我吃飯" {
		t.Fatalf("前提：顯示 %q", got)
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "我吃飯" {
		t.Fatalf("提交 %q", got)
	}
	st := s.model.Store
	for k := range st.Readings {
		if strings.Contains(k, "吃") {
			t.Errorf("不得記個人讀音：%q", k)
		}
	}
	for ph := range st.Phrases {
		if strings.Contains(ph, "吃") || strings.Contains(ph, "飯") {
			t.Errorf("不得晉升片語：%q", ph)
		}
	}
	if st.Uni["吃飯"] != 0 || st.Uses["吃飯"] != 0 {
		t.Errorf("不得計詞頻：%v %v", st.Uni["吃飯"], st.Uses["吃飯"])
	}
	if len(st.Rejects) != 0 {
		t.Errorf("不得記拒絕：%v", st.Rejects)
	}
}

// 沒有可換音的音節時不自動修正；未啟用 auto 時簽章與沒有此功能時相同。
func TestFuzzyAutoAddsNoWorkWithoutCandidates(t *testing.T) {
	s := autoSession(autoConfig(fuzzy.CCh), "ㄅㄚˋ")
	fzType(s, "ㄅㄚˋ")
	if s.autoFuzzed() {
		t.Fatal("沒有可換音的音節，不應有自動修正")
	}
	off := autoSession(fuzzy.Config{}, "ㄘ ㄈㄢˋ")
	if sig := off.signature(); strings.Contains(sig, "fuzzy:") {
		t.Fatalf("關閉時簽章不得含模糊音欄位：%q", sig)
	}
}

// 神經評分器不接觸自動修正的字：評分只依使用者打的音，自動修正的分段維持統計首選。
func TestFuzzyAutoNeverReachesTheNeuralScorer(t *testing.T) {
	var requests []rerank.Request
	const typed = "ㄘ ㄈㄢˋ"
	s := autoSession(autoConfig(fuzzy.CCh), typed)
	s.Rerank = rerank.Options{Weight: .1, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		requests = append(requests, r)
		return make([]float64, len(r.Candidates)), nil
	})}
	snap := fzType(s, typed)
	if snap.Text() != "吃飯" {
		t.Fatalf("顯示 %q", snap.Text())
	}
	for _, r := range requests {
		if !slices.Equal(r.Reading, zhuyin.ParseReading(typed)[:len(r.Reading)]) {
			t.Errorf("評分讀音必須是使用者打的音：%v", r.Reading)
		}
		for _, c := range r.Candidates {
			if strings.Contains(c.Text, "吃") {
				t.Errorf("近音候選 %q 不得送進評分器", c.Text)
			}
		}
	}
}

// 神經重排把某處的原音路徑改成別的字時：那一處照神經的選擇留著；近音路徑仍要贏過統計首選，
// 贏了的地方（另一處）照常改。神經不認得近音，所以不能否決近音的修正，但也不會被連帶換掉。
func TestFuzzyAutoKeepsTheNeuralChoiceOutsideTheBlockItChanges(t *testing.T) {
	const typed = "ㄘ ㄈㄢˋ ㄅㄚˋ"
	s := autoSession(autoConfig(fuzzy.CCh), typed)
	tailFiller := string(rune(0x6000 + 2)) // ㄅㄚˋ 的專屬單字
	s.Rerank = rerank.Options{Weight: 1, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			if strings.HasSuffix(c.Text, tailFiller) {
				scores[i] = 50 // 神經偏好結尾不是「爸」的原音候選
			}
		}
		return scores, nil
	})}
	got := fzType(s, typed).Text()
	if !strings.HasPrefix(got, "吃飯") {
		t.Fatalf("近音路徑領先統計首選很多，神經改選別處不影響這一處：%q", got)
	}
	if !strings.HasSuffix(got, tailFiller) || strings.Contains(got, "爸") {
		t.Fatalf("神經改選的結尾不得被換回統計首選：%q", got)
	}
}

// NeuralExtra：神經在這一處改過原音路徑時，近音要多領先一點才能蓋過；神經沒改的處不受影響。
func TestFuzzyAutoNeuralExtraOnlyAppliesWhereNeuralChanged(t *testing.T) {
	const typed = "ㄘ ㄈㄢˋ"
	neuralPrefersSecond := func() rerank.Options {
		return rerank.Options{Weight: 1, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
			scores := make([]float64, len(r.Candidates))
			if len(scores) > 1 {
				scores[1] = 50 // 神經偏好次佳的原音候選：改動落在這一處之內
			}
			return scores, nil
		})}
	}
	for _, c := range []struct {
		extra float64
		want  string
	}{{0, "吃飯"}, {1e6, ""}} {
		s := autoSession(autoConfig(fuzzy.CCh), typed)
		s.Opt.FuzzyAuto.NeuralExtra = c.extra
		s.Rerank = neuralPrefersSecond()
		got := fzType(s, typed).Text()
		if c.want != "" && got != c.want || c.want == "" && got == "吃飯" {
			t.Errorf("NeuralExtra=%g：顯示 %q", c.extra, got)
		}
	}
	// 神經沒有改這一處（沒接評分器）：額外門檻不適用。
	s := autoSession(autoConfig(fuzzy.CCh), typed)
	s.Opt.FuzzyAuto.NeuralExtra = 1e6
	if got := fzType(s, typed).Text(); got != "吃飯" {
		t.Errorf("神經沒改的處不該受 NeuralExtra 影響：%q", got)
	}
	if !neuralTouched([]decode.Seg{{Word: "a", Start: 0, End: 1}}, []decode.Seg{{Word: "b", Start: 0, End: 1}}, autoBlock{0, 1}) ||
		neuralTouched([]decode.Seg{{Word: "a", Start: 0, End: 1}}, []decode.Seg{{Word: "a", Start: 0, End: 1}}, autoBlock{0, 1}) ||
		neuralTouched([]decode.Seg{{Word: "a", Start: 0, End: 1}}, nil, autoBlock{0, 1}) {
		t.Error("neuralTouched")
	}
}

// 使用者鎖定永遠優先：與鎖定相交的處不改。手選原音的「吃」（它在詞庫裡也有 ㄘ 的讀音）之後，
// 更長的近音詞「吃飯」雖然也以「吃」開頭、字面相同，仍不得以換音路徑取代使用者的選擇。
func TestFuzzyAutoDoesNotTouchWhatTheUserLocked(t *testing.T) {
	const typed = "ㄘ ㄈㄢˋ"
	s := autoSession(autoConfig(fuzzy.CCh), typed, word{"吃", "ㄘ", 1e-6})
	if !fzTypeAuto(s, typed) {
		t.Fatal("前提：先自動修正")
	}
	fzSelect(t, s, 0, 1, "吃")
	if got := s.snapshot().Text(); got != "吃飯" {
		t.Fatalf("顯示 %q", got)
	}
	if s.autoFuzzed() {
		t.Fatalf("鎖定的位置不得再走自動修正的換音路徑：%+v", s.path)
	}
}

func fzTypeAuto(s *Session, typed string) bool {
	fzType(s, typed)
	return s.autoFuzzed()
}

// 精確讀音讀不出來的音節（ㄕㄨㄥˋ 不在音節表）：僅供選字時整段只能顯示原樣注音；
// auto 模式換成近音讀音，整段才有字；其他沒把握的處仍維持原音。
func TestFuzzyAutoReadsAnUnreadableSyllable(t *testing.T) {
	const typed = "ㄨㄛˇ ㄕㄨㄥˋ ㄒㄧㄣˋ"
	single := word{"送", "ㄙㄨㄥˋ", 5e-4} // 真實詞庫每個合法音節都有單字
	s := autoSession(autoConfig(fuzzy.SSh), typed, single)
	snap := fzType(s, typed)
	if snap.Text() != "我送信" {
		t.Fatalf("顯示 %q，應把不在音節表的 ㄕㄨㄥˋ 讀成 ㄙㄨㄥˋ", snap.Text())
	}
	var corrected string
	for _, seg := range snap.Preedit {
		if seg.Style == proto.StyleCorrected {
			corrected += seg.Text
		}
	}
	if corrected != "送信" {
		t.Errorf("自動修正的字要標示：%+v", snap.Preedit)
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "我送信" {
		t.Fatalf("提交 %q", got)
	}
	sel := fuzzySession(fuzzy.Config{}.With(fuzzy.SSh, fuzzy.Select), fillers(typed)...)
	sel.Opt.PrefixAware = false
	if got := fzType(sel, typed).Text(); got == "我送信" {
		t.Fatalf("僅供選字不得自動改字：%q", got)
	}
}

// 無法讀出的音節一定換音，但同一段其他位置照常看門檻：ㄧㄣ 只是小幅領先，維持原音。
func TestFuzzyAutoRequiredSwapDoesNotLowerTheBarElsewhere(t *testing.T) {
	const typed = "ㄕㄨㄥˋ ㄒㄧㄣˋ ㄧㄣ"
	extra := []word{{"嬴", "ㄧㄥ", 1e-2}, {"送", "ㄙㄨㄥˋ", 5e-4}}
	cfg := fuzzy.Config{}.With(fuzzy.SSh, fuzzy.Auto).With(fuzzy.EnEng, fuzzy.Auto)
	s := autoSession(cfg, typed, extra...)
	if got := fzType(s, typed).Text(); got != "送信因" {
		t.Fatalf("顯示 %q", got)
	}
}

// 改的只有那一處：區塊外保留原音路徑的詞，不因整段重新解碼而換掉不相干的同音詞。
func TestAutoSpliceKeepsTheBaseOutsideTheBlock(t *testing.T) {
	seg := func(w string, a, b int, m string) decode.Seg {
		return decode.Seg{Word: w, Start: a, End: b, Matched: m}
	}
	base := []decode.Seg{seg("油氣", 0, 2, ""), seg("成", 2, 3, ""), seg("琫", 3, 4, "")}
	final := []decode.Seg{seg("遊憩", 0, 2, ""), seg("成本", 2, 4, "ㄔㄥˊ ㄅㄣˇ")}
	got, ok := splice(base, final, []autoBlock{{2, 4}})
	if !ok || len(got) != 2 || got[0].Word != "油氣" || got[1].Word != "成本" {
		t.Fatalf("splice = %+v %v", got, ok)
	}
	// final 的詞跨過區塊邊界：拒絕，不拼出不連貫的路徑。
	if _, ok := splice(base, []decode.Seg{seg("遊憩成", 0, 3, ""), seg("本", 3, 4, "ㄅㄣˇ")}, []autoBlock{{2, 4}}); ok {
		t.Error("final 的詞跨過區塊邊界時應拒絕")
	}
	// 區塊沒有被 final 鋪滿：拒絕。
	if _, ok := splice(base, []decode.Seg{seg("遊憩", 0, 2, ""), seg("成", 2, 3, "")}, []autoBlock{{2, 4}}); ok {
		t.Error("區塊未鋪滿時應拒絕")
	}
}

// F13：既有的詞級修正（應該 ← ㄧㄣ ㄍㄞ）照舊：auto 不重複標成近音，字也不變。
func TestFuzzyAutoLeavesAnExistingCorrectionAlone(t *testing.T) {
	const typed = "ㄧㄣ ㄍㄞ"
	lex, sys := fuzzyLexicon(fillers(typed)...)
	lex.Add(lexicon.Entry{Surface: "應該", Reading: zhuyin.ParseReading("ㄧㄣ ㄍㄞ"), PRW: 1, Correction: true})
	s := New(personal.NewModel(lex, sys))
	s.Fuzzy = autoConfig(fuzzy.EnEng)
	s.Opt.PrefixAware = false
	if got := fzType(s, typed).Text(); got != "應該" {
		t.Fatalf("顯示 %q", got)
	}
	for _, g := range s.path {
		if g.Matched != "" {
			t.Errorf("既有修正的詞不得被標成近音修正：%+v", g)
		}
	}
}

// F33：評分器失敗、逾時或不存在時，自動修正照常（它只依統計路徑，與神經評分無關）。
func TestFuzzyAutoDoesNotDependOnTheScorer(t *testing.T) {
	const typed = "ㄘ ㄈㄢˋ"
	s := autoSession(autoConfig(fuzzy.CCh), typed)
	s.Rerank = rerank.Options{Weight: .1, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		return nil, errScorer
	})}
	if got := fzType(s, typed).Text(); got != "吃飯" {
		t.Fatalf("評分器失敗時顯示 %q", got)
	}
}

var errScorer = errorString("scorer unavailable")

type errorString string

func (e errorString) Error() string { return string(e) }

// F35：規則、模式與門檻都在簽章裡；換設定後下一次重新解碼就採用新設定，不沿用舊決策。
func TestFuzzyAutoSignatureCoversPolicy(t *testing.T) {
	const typed = "ㄘ ㄈㄢˋ"
	s := autoSession(autoConfig(fuzzy.CCh), typed)
	if fzType(s, typed).Text() != "吃飯" {
		t.Fatal("前提：先自動改成吃飯")
	}
	sig := s.signature()
	s.Opt.FuzzyAuto.Margin = 1e6
	if s.signature() == sig {
		t.Fatal("門檻不在簽章裡，換門檻會沿用舊決策")
	}
	s.refresh()
	if got := s.snapshot().Text(); got == "吃飯" {
		t.Fatalf("換成不可能達到的門檻後仍顯示 %q", got)
	}
	s.Fuzzy = fuzzy.Config{}.With(fuzzy.CCh, fuzzy.Select)
	if strings.Contains(s.signature(), "fuzzy:") {
		t.Fatal("沒有 auto 配對時簽章不得含模糊音欄位")
	}
}

// F36：敏感欄位與暫停學習。auto 只是顯示，什麼都不學；提交的文字等於畫面上的文字。
func TestFuzzyAutoInSensitiveFieldsAndPausedLearning(t *testing.T) {
	const typed = "ㄨㄛˇ ㄘ ㄈㄢˋ"
	for _, mode := range []string{"private", "paused"} {
		s := autoSession(autoConfig(fuzzy.CCh), typed)
		req := func(r proto.Request) proto.Snapshot {
			r.Private = mode == "private"
			return s.Handle(r)
		}
		if mode == "paused" {
			req(proto.Request{Type: proto.Pause, On: true})
		}
		var snap proto.Snapshot
		for _, y := range strings.Fields(typed) {
			for _, k := range layout.Standard.Keys(zhuyin.Syllable(y)) {
				snap = req(proto.Request{Type: proto.Key, Key: string(k)})
			}
		}
		if snap.Text() != "我吃飯" {
			t.Fatalf("%s：顯示 %q", mode, snap.Text())
		}
		if got := req(proto.Request{Type: proto.Commit}).Commit; got != "我吃飯" {
			t.Fatalf("%s：提交 %q，應等於畫面上的文字", mode, got)
		}
		st := s.model.Store
		if len(st.Readings) != 0 || len(st.Phrases) != 0 || len(st.Picked) != 0 || len(st.Uni) != 0 || len(st.Uses) != 0 {
			t.Errorf("%s：不得學習任何東西：%+v", mode, st)
		}
	}
}

// fzLiteral 在目前位置插入一個英數字面符號（切英文、打字、切回注音），與使用者實際操作相同。
func fzLiteral(s *Session, text string) {
	s.Handle(proto.Request{Type: proto.Toggle})
	for _, k := range text {
		key(s, string(k))
	}
	s.Handle(proto.Request{Type: proto.Toggle})
}

// F36：標點、英數等字面符號是解碼邊界，近音修正不跨過它，也不把兩側併成一個詞。
func TestFuzzyAutoDoesNotCrossLiterals(t *testing.T) {
	const typed = "ㄘ ㄈㄢˋ"
	s := autoSession(autoConfig(fuzzy.CCh), typed)
	fzType(s, "ㄘ")
	fzLiteral(s, "1")
	snap := fzType(s, "ㄈㄢˋ")
	if !strings.Contains(snap.Text(), "1") || len(s.literals) != 1 {
		t.Fatalf("字面符號要留在畫面上：%q（literals=%v）", snap.Text(), s.literals)
	}
	if strings.Contains(snap.Text(), "吃飯") {
		t.Fatalf("不得跨過字面符號把兩側併成一個詞：%q", snap.Text())
	}
	// 同一段（不被字面符號隔開）照常自動修正，字面符號之前的一段不受影響。
	s2 := autoSession(autoConfig(fuzzy.CCh), typed)
	fzType(s2, "ㄨㄛˇ")
	fzLiteral(s2, "1")
	got := fzType(s2, typed).Text()
	if !strings.Contains(got, "吃飯") || !strings.HasPrefix(got, "我1") {
		t.Fatalf("字面符號後的整段仍應自動修正：%q", got)
	}
}

// 診斷：每一處自動修正都說明打的音、實際讀音、用到的配對、通道成本與當時的領先分數和門檻。
func TestAutoChangesExplainEachCorrection(t *testing.T) {
	const typed = "ㄘ ㄈㄢˋ"
	s := autoSession(autoConfig(fuzzy.CCh), typed)
	fzType(s, typed)
	got := s.AutoChanges()
	if len(got) != 1 {
		t.Fatalf("應有一處：%+v", got)
	}
	c := got[0]
	if c.Start != 0 || c.End != 2 || c.Text != "吃飯" || c.Typed != "ㄘ ㄈㄢˋ" || c.Matched != "ㄔ ㄈㄢˋ" ||
		!slices.Equal(c.Rules, []string{"c-ch"}) || c.Cost != fuzzy.SwapCost || c.Required {
		t.Fatalf("內容不對：%+v", c)
	}
	if want := s.Opt.FuzzyAuto.Threshold(2, 1); c.Threshold != want || c.Gain < c.Threshold {
		t.Fatalf("領先 %g 應不低於門檻 %g（預期門檻 %g）", c.Gain, c.Threshold, want)
	}
	// 回傳的是複本；提交後清空。
	got[0].Text = "改"
	if s.AutoChanges()[0].Text != "吃飯" {
		t.Fatal("AutoChanges 必須回傳複本")
	}
	s.Handle(proto.Request{Type: proto.Commit})
	if s.AutoChanges() != nil {
		t.Fatalf("提交後不應殘留：%+v", s.AutoChanges())
	}
}

// 必須換音的音節（精確讀音讀不出來）：記錄為 Required，不看門檻。
func TestAutoChangesMarkRequiredSwaps(t *testing.T) {
	const typed = "ㄨㄛˇ ㄕㄨㄥˋ ㄒㄧㄣˋ"
	s := autoSession(autoConfig(fuzzy.SSh), typed, word{"送", "ㄙㄨㄥˋ", 5e-4})
	fzType(s, typed)
	var at *AutoChange
	for _, c := range s.AutoChanges() {
		if c.Start <= 1 && 1 < c.End {
			at = &c
		}
	}
	if at == nil || !strings.Contains(at.Typed, "ㄕㄨㄥˋ") || !strings.Contains(at.Matched, "ㄙㄨㄥˋ") || !slices.Equal(at.Rules, []string{"s-sh"}) {
		t.Fatalf("應記錄 ㄕㄨㄥˋ→ㄙㄨㄥˋ（s-sh）：%+v", s.AutoChanges())
	}
	if at.Required && (at.Gain != 0 || at.Threshold != 0) {
		t.Fatalf("必須換音不經過門檻：%+v", at)
	}
}

// 沒有自動修正時沒有紀錄：選字模式、原音首選、鎖定之後。
func TestAutoChangesEmptyWhenNothingChanged(t *testing.T) {
	const typed = "ㄘ ㄈㄢˋ"
	sel := fuzzySession(fuzzy.Config{}.With(fuzzy.CCh, fuzzy.Select), fillers(typed)...)
	sel.Opt.PrefixAware = false
	fzType(sel, typed)
	if sel.AutoChanges() != nil {
		t.Fatalf("僅供選字不得有紀錄：%+v", sel.AutoChanges())
	}
	s := autoSession(autoConfig(fuzzy.CCh), typed, word{"吃", "ㄘ", 1e-6})
	fzType(s, typed)
	fzSelect(t, s, 0, 1, "吃")
	if s.autoFuzzed() || s.AutoChanges() != nil {
		t.Fatalf("鎖定後不得有自動修正紀錄：%+v", s.AutoChanges())
	}
}
