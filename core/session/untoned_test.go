package session

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// newTonelessSession 是聲調選填（#150）的測試詞庫：同音不同調（會議／回憶、你／泥／逆）、
// 切分歧義（先／西安）與只有一個聲調有字的音節。toneless 決定是否開啟。
func newTonelessSession(toneless bool) *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"你", "ㄋㄧˇ", 1e-2}, {"泥", "ㄋㄧˊ", 2e-4}, {"逆", "ㄋㄧˋ", 1e-4}, {"妮", "ㄋㄧ", 5e-5},
		{"好", "ㄏㄠˇ", 5e-3}, {"號", "ㄏㄠˋ", 1e-3}, {"豪", "ㄏㄠˊ", 2e-4}, {"你好", "ㄋㄧˇ ㄏㄠˇ", 2e-3},
		{"先", "ㄒㄧㄢ", 2e-3}, {"現", "ㄒㄧㄢˋ", 3e-3}, {"西", "ㄒㄧ", 1e-3}, {"安", "ㄢ", 1e-3}, {"西安", "ㄒㄧ ㄢ", 1e-4},
		{"在", "ㄗㄞˋ", 8e-3}, {"現在", "ㄒㄧㄢˋ ㄗㄞˋ", 2e-3},
		{"會", "ㄏㄨㄟˋ", 6e-3}, {"回", "ㄏㄨㄟˊ", 3e-3}, {"議", "ㄧˋ", 5e-4}, {"憶", "ㄧˋ", 2e-4},
		{"會議", "ㄏㄨㄟˋ ㄧˋ", 6e-4}, {"回憶", "ㄏㄨㄟˊ ㄧˋ", 3e-4}, {"會意", "ㄏㄨㄟˋ ㄧˋ", 1e-5},
		{"我", "ㄨㄛˇ", 1e-2}, {"們", "ㄇㄣ˙", 5e-3}, {"門", "ㄇㄣˊ", 1e-3}, {"我們", "ㄨㄛˇ ㄇㄣ˙", 3e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.Toneless = toneless
	return s
}

// typeBare 逐鍵打注音符號（標準排列），不補聲調：「ㄋㄧㄏㄠ」是 s u c l 四個鍵。
// 聲調符號（ˊˇˋ˙）照打；空白打 " "。
func typeBare(s *Session, zhuyinText string) proto.Snapshot {
	var snap proto.Snapshot
	for _, r := range zhuyinText {
		k := " "
		if r != ' ' {
			k = string([]rune(layout.Standard.Keys(zhuyin.Syllable(string(r))))[0])
		}
		snap = s.Handle(proto.Request{Type: proto.Key, Key: k})
	}
	return snap
}

func TestTonelessOffKeepsReplacingInitials(t *testing.T) {
	s := newTonelessSession(false)
	if snap := typeBare(s, "ㄋㄧㄏ"); snap.Text() != "ㄏㄧ" {
		t.Fatalf("關閉時新聲母照舊取代舊聲母：%q", snap.Text())
	}
}

func TestTonelessSplitsAtNextInitial(t *testing.T) {
	s := newTonelessSession(true)
	if snap := typeBare(s, "ㄋㄧㄏㄠ"); snap.Text() != "你ㄏㄠ" {
		t.Fatalf("ㄏ 接不上 ㄋㄧ，ㄋㄧ 不打聲調就完成：%q", snap.Text())
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "你好" {
		t.Fatalf("Enter 時未打完的 ㄏㄠ 也不打聲調就完成：%q", snap.Commit)
	}
}

func TestTonelessToneIsHardConstraint(t *testing.T) {
	s := newTonelessSession(true)
	typeBare(s, "ㄋㄧˋㄏㄠ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "逆好" && snap.Commit != "逆號" {
		t.Fatalf("有打聲調的 ㄋㄧˋ 只能是逆：%q", snap.Commit)
	} else if snap.Commit == "你好" {
		t.Fatal("不該忽略聲調")
	}
}

func TestTonelessSegmentationAndToneFix(t *testing.T) {
	s := newTonelessSession(true)
	typeBare(s, "ㄒㄧㄢㄗㄞ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "現在" {
		t.Fatalf("ㄒㄧㄢ ㄗㄞ：%q", snap.Commit)
	}
	// ㄒㄧㄢ 照最長的音節讀；要西安就在 ㄒㄧ 後面補聲調（空白是一聲），那個音節就固定了。
	typeBare(s, "ㄒㄧㄢ")
	if snap := key(s, proto.KeyEnter); snap.Commit == "西安" {
		t.Fatalf("ㄒㄧㄢ 是一個音節：%q", snap.Commit)
	}
	typeBare(s, "ㄒㄧ ㄢ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "西安" {
		t.Fatalf("補了一聲的 ㄒㄧ 後面接 ㄢ：%q", snap.Commit)
	}
}

func TestTonelessIncompleteSyllableStillReplaced(t *testing.T) {
	s := newTonelessSession(true)
	// ㄋ 自己不是音節：照原本的規則，ㄏ 取代 ㄋ（打錯聲母可以直接改）。
	if snap := typeBare(s, "ㄋㄏㄠ"); snap.Text() != "ㄏㄠ" {
		t.Fatalf("不完整的音節照舊取代：%q", snap.Text())
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "好" {
		t.Fatalf("Enter：%q", snap.Commit)
	}
}

func TestTonelessSpaceIsFirstTone(t *testing.T) {
	s := newTonelessSession(true)
	typeBare(s, "ㄒㄧㄢ ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "先" {
		t.Fatalf("沒打完的音節按空白是一聲：%q", snap.Commit)
	}
}

func TestTonelessCandidatesMixTones(t *testing.T) {
	s := newTonelessSession(true)
	typeBare(s, "ㄏㄨㄟㄧ")
	snap := key(s, proto.KeyDown) // 先把未打完的 ㄧ 完成，再開候選
	if snap.Candidates == nil {
		t.Fatalf("↓ 應開候選：%+v", snap.Preedit)
	}
	got := map[string]bool{}
	for _, it := range snap.Candidates.Items {
		got[it.Text] = true
	}
	if !got["會議"] && !got["議"] {
		t.Fatalf("候選：%+v", snap.Candidates.Items)
	}
	key(s, proto.KeyEscape)
	key(s, proto.KeyHome)
	snap = key(s, proto.KeyDown)
	got = map[string]bool{}
	for _, it := range snap.Candidates.Items {
		got[it.Text] = true
	}
	if !got["會議"] || !got["回憶"] {
		t.Fatalf("不同聲調的詞都在候選裡：%+v", snap.Candidates.Items)
	}
}

func TestTonelessPunctuationFinishesSyllable(t *testing.T) {
	s := newTonelessSession(true)
	typeBare(s, "ㄋㄧㄏㄠ")
	key(s, "<") // Shift＋, 是「，」
	if snap := key(s, proto.KeyEnter); snap.Commit != "你好，" {
		t.Fatalf("標點之前 ㄏㄠ 完成，不留成注音：%q", snap.Commit)
	}
}

func TestTonelessDisablesMixedInput(t *testing.T) {
	s := newTonelessSession(true)
	s.MixedInput = true
	typeBare(s, "ㄋㄧㄏㄠ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "你好" {
		t.Fatalf("開著混打的設定也照注音：%q", snap.Commit)
	}
}

func TestTonelessLearnsLexiconReading(t *testing.T) {
	s := newTonelessSession(true)
	typeBare(s, "ㄨㄛㄇㄣ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "我們" {
		t.Fatalf("ㄨㄛ ㄇㄣ：%q", snap.Commit)
	}
	b, err := json.Marshal(s.model.Store)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(string(b), zhuyin.UntonedMark) {
		t.Fatalf("個人資料不能有沒打聲調的記號：%s", b)
	}
	if !strings.Contains(string(b), "我們") {
		t.Fatalf("照常學習：%s", b)
	}
}

// 選一次就學到（同 RIME 的使用者詞典）：學習與修正紀錄收詞庫讀音，下次不打聲調也照選過的出。
func TestTonelessPickIsLearnedWithLexiconReading(t *testing.T) {
	s := newTonelessSession(true)
	typeBare(s, "ㄏㄨㄟㄧ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "會議" {
		t.Fatalf("沒選過時照詞頻：%q", snap.Commit)
	}
	typeBare(s, "ㄏㄨㄟㄧ")
	key(s, proto.KeyHome)
	snap := key(s, proto.KeyDown)
	picked := false
	for _, it := range snap.Candidates.Items {
		if it.Text == "回憶" {
			s.Handle(proto.Request{Type: proto.Select, Index: it.ID})
			picked = true
		}
	}
	if !picked || key(s, proto.KeyEnter).Commit != "回憶" {
		t.Fatal("選回憶")
	}
	if !s.model.Store.Readings["回憶\tㄏㄨㄟˊ ㄧˋ"] {
		t.Fatalf("手選的讀音是詞庫讀音：%v", s.model.Store.Readings)
	}
	for k := range s.model.Store.Corrections {
		if strings.ContainsRune(k, zhuyin.UntonedMark) {
			t.Fatalf("修正紀錄不能有記號：%q", k)
		}
	}
	typeBare(s, "ㄏㄨㄟㄧ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "回憶" {
		t.Fatalf("選過之後不打聲調也出回憶：%q", snap.Commit)
	}
}

// 路徑上的詞是自動修正詞條（「你號」只有修正讀音），提交時換不回詞庫讀音，那段音節仍帶記號。
// 包在裡面的手選（「號」）與被推翻的自動修正（選「你」推翻「你號」）都不能把帶記號的讀音寫進個人資料。
// 修正前：Readings 有「號\tㄏㄠ?」、Picked 有「ㄏㄠ?」；Rejects 有「你號\tㄋㄧ? ㄏㄠ?」。
func TestTonelessUnresolvedReadingIsNotLearned(t *testing.T) {
	for _, tc := range []struct {
		name   string
		uni    float64
		move   string
		pick   string
		shown  string
		reject bool
	}{
		{"包住手選的修正詞", 1e-3, proto.KeyLeft, "號", "你好", false},
		{"推翻自動修正", 5e-2, proto.KeyHome, "你", "你號", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTonelessSession(true)
			s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: "你號", Reading: zhuyin.ParseReading("ㄋㄧˇ ㄏㄠˋ"), PRW: 1, Correction: true})
			s.model.LM.(*lm.Mem).SetUni("你號", tc.uni)
			typeBare(s, "ㄋㄧㄏㄠ")
			key(s, tc.move) // 先完成未打完的 ㄏㄠ
			if got := s.snapshot().Text(); got != tc.shown {
				t.Fatalf("選字前顯示 %q", got)
			}
			snap := key(s, proto.KeyDown)
			picked := false
			for _, it := range snap.Candidates.Items {
				if it.Text == tc.pick {
					s.Handle(proto.Request{Type: proto.Select, Index: it.ID})
					picked = true
					break
				}
			}
			if !picked {
				t.Fatalf("候選沒有 %q：%+v", tc.pick, snap.Candidates.Items)
			}
			if len(s.path) != 1 || s.path[0].Word != "你號" || !s.path[0].Correction {
				t.Fatalf("前提：修正詞包住手選：%+v", s.path)
			}
			if got := len(s.rejected) > 0 && s.rejected[0].Correction; got != tc.reject {
				t.Fatalf("前提：推翻自動修正 %v：%+v", tc.reject, s.rejected)
			}
			if c := key(s, proto.KeyEnter).Commit; c != "你號" {
				t.Fatalf("送出 %q", c)
			}
			b, err := json.Marshal(s.model.Store)
			if err != nil {
				t.Fatal(err)
			}
			if strings.ContainsRune(string(b), zhuyin.UntonedMark) {
				t.Fatalf("個人資料不能有沒打聲調的記號：%s", b)
			}
		})
	}
}

// withNiHaoCorrection 在聲調選填的測試詞庫加一個只有修正讀音的詞「你號」（ㄋㄧˇ ㄏㄠˋ），uni 決定它是否勝過「你好」。
func withNiHaoCorrection(s *Session, uni float64) *Session {
	s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: "你號", Reading: zhuyin.ParseReading("ㄋㄧˇ ㄏㄠˋ"), PRW: 1, Correction: true})
	s.model.LM.(*lm.Mem).SetUni("你號", uni)
	return s
}

// pickHere 在游標處開候選、選 word。
func pickHere(t *testing.T, s *Session, word string) {
	t.Helper()
	snap := key(s, proto.KeyDown)
	if snap.Candidates != nil {
		for _, it := range snap.Candidates.Items {
			if it.Text == word {
				s.Handle(proto.Request{Type: proto.Select, Index: it.ID})
				return
			}
		}
	}
	t.Fatalf("候選沒有 %q：%+v", word, snap.Candidates)
}

// 包住手選的詞換不回詞庫讀音時，手選本身用它自己的詞庫讀音照樣學到（跟打聲調時一樣記個人讀音、單字手選與更正）；
// 包住它的修正詞讀音不明，仍不學。修正前（cf5371a）手選完全不學。
func TestTonelessEnclosedPickUsesItsOwnReading(t *testing.T) {
	s := withNiHaoCorrection(newTonelessSession(true), 1e-3)
	typeBare(s, "ㄋㄧㄏㄠ")
	key(s, proto.KeyLeft)
	pickHere(t, s, "號")
	if len(s.path) != 1 || s.path[0].Word != "你號" || !s.path[0].Correction {
		t.Fatalf("前提：修正詞包住手選：%+v", s.path)
	}
	if c := key(s, proto.KeyEnter).Commit; c != "你號" {
		t.Fatalf("送出 %q", c)
	}
	st := s.model.Store
	if !st.Readings["號\tㄏㄠˋ"] {
		t.Errorf("手選的個人讀音：%v", st.Readings)
	}
	if !slices.Contains(st.Picked["ㄏㄠˋ"], "號") {
		t.Errorf("單字手選：%v", st.Picked)
	}
	found := false
	for _, c := range st.Corrections {
		found = found || c.Word == "號" && c.Reading == "ㄏㄠˋ"
	}
	if !found {
		t.Errorf("手選的更正：%v", st.Corrections)
	}
	if st.Uni["你號"] != 0 {
		t.Errorf("讀音不明的修正詞不學：%v", st.Uni)
	}
	b, _ := json.Marshal(st)
	if strings.ContainsRune(string(b), zhuyin.UntonedMark) {
		t.Fatalf("個人資料不能有沒打聲調的記號：%s", b)
	}
}

// 推翻同一個自動修正兩次就停用它（RejectsToDisable）。聲調選填時拒絕次數要記在修正詞條自己的讀音上：
// 修正前（cf5371a）選「你好」記在「你號\tㄋㄧˇ ㄏㄠˇ」、選「你」不計，第三次照樣顯示自動修正。打聲調時行為不變。
func TestTonelessRejectingAutoCorrectionTwiceDisablesIt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		toneless bool
		typed    string
		pick     string
	}{
		{"打聲調選你", false, "ㄋㄧˇㄏㄠˋ", "你"},
		{"不打聲調選你", true, "ㄋㄧㄏㄠ", "你"},
		{"不打聲調選你好", true, "ㄋㄧㄏㄠ", "你好"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := withNiHaoCorrection(newTonelessSession(tc.toneless), 5e-2)
			corrected := func() bool {
				return slices.ContainsFunc(s.path, func(g decode.Seg) bool { return g.Word == "你號" && g.Correction })
			}
			for round := range 2 {
				typeBare(s, tc.typed)
				key(s, proto.KeyHome)
				if !corrected() {
					t.Fatalf("第 %d 次：前提是顯示自動修正：%+v", round+1, s.path)
				}
				pickHere(t, s, tc.pick)
				key(s, proto.KeyEnter)
			}
			if got := s.model.Store.Rejects["你號\tㄋㄧˇ ㄏㄠˋ"]; got != 2 {
				t.Errorf("拒絕次數記在修正讀音上：%v", s.model.Store.Rejects)
			}
			typeBare(s, tc.typed)
			key(s, proto.KeyHome)
			if corrected() {
				t.Fatalf("推翻兩次後應停用：%+v", s.path)
			}
		})
	}
}

// 不打聲調的段落沒有一組共用的帶調讀音，不送神經評分器，照統計引擎（以「聲調未知」送進現有模型反而變差）。
// 同一段組字裡打了聲調、被標點隔開的段落照樣評分；
// Enter 完成最後一個沒打聲調的音節時整段重新解碼，那一段神經選的字也要留著，不換回統計結果。
func TestTonelessNeuralSkipsUntonedSegmentsAndKeepsTonedOnes(t *testing.T) {
	s := newTonelessSession(true)
	s.Opt.PrefixAware = false
	var readings []string
	s.Rerank = rerank.Options{Weight: .1, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		readings = append(readings, zhuyin.Key(r.Reading))
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			if strings.Contains(c.Text, "會意") {
				scores[i] = 1000
			}
		}
		return scores, nil
	})}
	typeBare(s, "ㄏㄨㄟˋㄧˋ")
	if snap := s.View(); snap.Text() != "會意" {
		t.Fatalf("打了聲調的段落照樣評分：%q %v", snap.Text(), readings)
	}
	key(s, "<") // ，
	typeBare(s, "ㄋㄧㄏㄠ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "會意，你好" {
		t.Fatalf("Enter 完成最後一個音節後，前面神經選的字留著：%q %v", snap.Commit, readings)
	}
	for _, r := range readings {
		if strings.ContainsRune(r, zhuyin.UntonedMark) {
			t.Fatalf("沒打聲調的讀音不送評分器：%v", readings)
		}
	}
}

// 聲調選填停用混打，只保留「我的詞彙」裡的英文詞（負責人決定）：一串注音鍵恰好是使用者加的英文詞時，
// 接空白、Enter 或標點照字面送出。沒加的英文、打到一半、退格改過的都照注音。
func TestTonelessKeepsVocabEnglish(t *testing.T) {
	for _, tc := range []struct{ keys, commit string }{
		{"ls ", "ls "},
		{"ls", "ls"},
		{"kubectl ", "kubectl "},
		{"git-lfs ", "git-lfs "},
		{"ipv6 ", "ipv6 "}, // 英文詞裡的聲調鍵
		{"ls<", "ls，"},
		{"ji3ls ", "我ls "},
	} {
		s := englishVocabSession(t, true, "ls", "kubectl", "git-lfs", "ipv6")
		s.Toneless = true
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.commit {
			t.Errorf("%q 送出 %q，要 %q", tc.keys, got, tc.commit)
		}
	}
	for _, tc := range []struct{ keys string }{{"hello "}, {"lsx\b "}, {"l s "}} {
		s := englishVocabSession(t, true, "ls")
		s.Toneless = true
		for _, k := range tc.keys {
			if k == '\b' {
				key(s, proto.KeyBackspace)
				continue
			}
			key(s, string(k))
		}
		if got := key(s, proto.KeyEnter).Commit; got == "ls " || got == "hello " {
			t.Errorf("%q 不是加過的英文詞，照注音：%q", tc.keys, got)
		}
	}
	s := englishVocabSession(t, true)
	s.Toneless = true
	mixedType(s, "ls ")
	if got := key(s, proto.KeyEnter).Commit; got == "ls " {
		t.Errorf("沒加英文詞時照注音：%q", got)
	}
}

// 負責人 2026-10-22：聲調選填時組字中的 ' 只切音節、不出頓號；沒有組字時照舊是頓號；關閉時照舊。
func TestTonelessQuoteSeparatesSyllables(t *testing.T) {
	s := newTonelessSession(true)
	typeBare(s, "ㄒㄧ")
	if snap := key(s, "'"); !snap.Handled || snap.Text() != "西" {
		t.Fatalf("' 把 ㄒㄧ 不打聲調完成：%q", snap.Text())
	}
	typeBare(s, "ㄢ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "西安" {
		t.Fatalf("ㄒㄧ'ㄢ：%q", snap.Commit)
	}
	// 剛打完聲調、沒有未打完的注音：已經是音節邊界，' 什麼都不做，不多出頓號。
	typeBare(s, "ㄋㄧˇ")
	if snap := key(s, "'"); !snap.Handled || snap.Text() != "你" {
		t.Fatalf("聲調之後的 '：%q", snap.Text())
	}
	// 注音還不是完整音節（ㄅ）：不完成、不出頓號，接著打的注音照常。
	typeBare(s, "ㄏ")
	if snap := key(s, "'"); snap.Text() != "你ㄏ" {
		t.Fatalf("不完整音節的 '：%q", snap.Text())
	}
	typeBare(s, "ㄠ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "你好" {
		t.Fatalf("送出：%q", snap.Commit)
	}
	// 沒有組字時照舊是頓號。
	if snap := key(s, "'"); !snap.Handled || snap.Text() != "、" {
		t.Fatalf("沒有組字時 ' 是頓號：%q", snap.Text())
	}
	key(s, proto.KeyEnter)
	// 候選開著時 ' 照舊被候選窗吃掉，不切音節。
	typeBare(s, "ㄏㄨㄟㄧ")
	key(s, proto.KeyDown)
	if snap := key(s, "'"); snap.Candidates == nil {
		t.Fatal("候選開著時 ' 不關候選")
	}
	// 關閉聲調選填：組字中的 ' 照舊是頓號。
	off := newTonelessSession(false)
	typeBare(off, "ㄋㄧˇ")
	key(off, "'")
	if snap := key(off, proto.KeyEnter); snap.Commit != "你、" {
		t.Fatalf("關閉時 ' 是頓號：%q", snap.Commit)
	}
}

// 聲調選填只在標準排列生效：倚天 41 鍵的 ' 是 ㄘ，許氏一鍵多用；設定開著也照原本的打法。
func TestTonelessOnlyOnStandardLayout(t *testing.T) {
	for _, l := range []layout.Layout{layout.ETen, layout.Hsu} {
		s := newTonelessSession(true)
		s.SetLayout(l)
		var snap proto.Snapshot
		for _, r := range "ㄋㄧㄏ" {
			snap = key(s, string([]rune(l.Keys(zhuyin.Syllable(string(r))))[0]))
		}
		if strings.Contains(snap.Text(), "你") {
			t.Errorf("%s：不打聲調不生效：%q", l.Name(), snap.Text())
		}
	}
}

func TestTonelessDisplaysWithoutMark(t *testing.T) {
	s := newTonelessSession(true)
	// ㄈㄧㄠ 是音節表內的音節，但這份詞庫沒有字：原樣留下，不顯示內部記號。
	snap := typeBare(s, "ㄈㄧㄠㄋㄧ")
	if strings.ContainsRune(snap.Text(), zhuyin.UntonedMark) {
		t.Fatalf("畫面不顯示記號：%q", snap.Text())
	}
	snap = key(s, proto.KeyEnter)
	if strings.ContainsRune(snap.Commit, zhuyin.UntonedMark) || !strings.HasSuffix(snap.Commit, "你") {
		t.Fatalf("送出：%q", snap.Commit)
	}
}
