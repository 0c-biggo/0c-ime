package session

import (
	"encoding/json"
	"strings"
	"testing"

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

// 不打聲調的段落沒有一組共用的帶調讀音，不送神經評分器，照統計引擎（以「聲調未知」送進現有模型反而變差，
// 見 data/research/toneless-input-20261022）。同一段組字裡打了聲調、被標點隔開的段落照樣評分；
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
