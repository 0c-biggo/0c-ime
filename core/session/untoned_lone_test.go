package session

import (
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// newLoneSession 是 #360 的測試詞庫：詞頻與句首配對照 0.2.13 資料包的實際數字。語料以非漢字切成片段，
// 句首配對其實是「片段開頭」，接在阿拉伯數字後面的字（號、億）在那裡特別多，跟字本身常不常用是兩回事。
func newLoneSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r     string
		uni, bos float64
		prw      float64
	}{
		{"好", "ㄏㄠˇ", 8.229e-4, 3.540e-4, 1}, {"好", "ㄏㄠˋ", 8.229e-4, 3.540e-4, 0.0657},
		{"號", "ㄏㄠˋ", 1.877e-4, 6.310e-4, 1}, {"號", "ㄏㄠˊ", 1.877e-4, 6.310e-4, 0.0212},
		{"豪", "ㄏㄠˊ", 6.758e-5, 3.508e-5, 1}, {"浩", "ㄏㄠˋ", 2.597e-5, 5.765e-6, 1},
		{"一", "ㄧ", 4.196e-3, 4.090e-4, 1}, {"一", "ㄧˊ", 4.196e-3, 4.090e-4, 0.5}, {"一", "ㄧˋ", 4.196e-3, 4.090e-4, 0.5},
		{"以", "ㄧˇ", 2.382e-3, 1.501e-3, 1}, {"億", "ㄧˋ", 3.071e-4, 1.125e-3, 1}, {"已", "ㄧˇ", 1.218e-3, 5.461e-4, 1},
		{"是", "ㄕˋ", 8.586e-3, 2.489e-3, 1}, {"時", "ㄕˊ", 1.578e-3, 7.836e-4, 1}, {"使", "ㄕˇ", 4.711e-4, 2.850e-4, 1},
		{"我", "ㄨㄛˇ", 5.418e-3, 1.735e-3, 1}, {"沃", "ㄨㄛˋ", 1.370e-5, 4.357e-5, 1}, {"窩", "ㄨㄛ", 1.615e-5, 1.968e-5, 1},
		{"幾", "ㄐㄧˇ", 4.0e-4, 3.0e-4, 1}, {"你", "ㄋㄧˇ", 3.0e-3, 2.0e-3, 1},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: e.prw})
		sys.SetUni(e.w, e.uni)
		sys.SetBi(lm.BOS, e.w, e.bos)
	}
	sys.SetBi("幾", "號", 0.05)
	sys.SetBi("幾", "好", 1e-4)
	sys.SetBi("你", "好", 0.05)
	sys.SetBi("你", "號", 1e-5)
	s := New(personal.NewModel(lex, sys))
	s.Toneless = true
	return s
}

// loneCandidates 打一串注音（不補聲調）、按 ↓，回傳單字候選（不含最後的原樣注音）。
func loneCandidates(t *testing.T, s *Session, zhuyinText string) []string {
	t.Helper()
	typeBare(s, zhuyinText)
	snap := key(s, proto.KeyDown)
	if snap.Candidates == nil {
		t.Fatalf("%s：↓ 應開候選：%+v", zhuyinText, snap.Preedit)
	}
	out := texts(snap.Candidates.Items)
	return out[:len(out)-1]
}

// 單獨一個不打聲調的音節：候選照字的常用程度排，不看句首配對（#360）。
func TestLoneUntonedSyllableRanksByFrequency(t *testing.T) {
	for _, c := range []struct {
		typed string
		want  []string
	}{
		{"ㄏㄠ", []string{"好", "號", "豪", "浩"}},
		{"ㄧ", []string{"一", "以", "已", "億"}},
		{"ㄕ", []string{"是", "時", "使"}},
		{"ㄨㄛ", []string{"我", "窩", "沃"}},
	} {
		if got := loneCandidates(t, newLoneSession(), c.typed); !slices.Equal(got, c.want) {
			t.Errorf("%s 的候選 %v，應為 %v", c.typed, got, c.want)
		}
		// 還沒開候選時組字條顯示、Enter 送出的也是同一個字。
		s := newLoneSession()
		typeBare(s, c.typed)
		if snap := key(s, proto.KeyEnter); snap.Commit != c.want[0] {
			t.Errorf("%s 按 Enter 送出 %q，應為 %q", c.typed, snap.Commit, c.want[0])
		}
		// 手機的候選列：注音還沒完成時預覽的候選是同一份。
		s = newLoneSession()
		typeBare(s, c.typed)
		if list := s.PreviewPending(); list == nil || list.Items[0].Text != c.want[0] {
			t.Errorf("%s 的預覽候選 %+v，第一個應為 %q", c.typed, list, c.want[0])
		}
	}
}

// 打了聲調的行為不變：照舊由句首的語言模型排（ㄏㄠˋ 號在好前面、ㄧˋ 億在一前面）。
func TestLoneTonedSyllableKeepsSentenceStartOrder(t *testing.T) {
	for _, c := range []struct {
		typed string
		want  []string
	}{
		{"ㄏㄠˋ", []string{"號", "好", "浩"}},
		{"ㄏㄠˇ", []string{"好"}},
		{"ㄧˋ", []string{"億", "一"}},
		{"ㄧˇ", []string{"以", "已"}},
		{"ㄧ ", []string{"一"}}, // 空白是一聲
	} {
		for _, toneless := range []bool{true, false} {
			s := newLoneSession()
			s.Toneless = toneless
			if got := loneCandidates(t, s, c.typed); !slices.Equal(got, c.want) {
				t.Errorf("%s（聲調選填 %v）的候選 %v，應為 %v", c.typed, toneless, got, c.want)
			}
		}
	}
}

// 有前後文時照舊由語言模型決定：幾＋ㄏㄠ 是號、你＋ㄏㄠ 是好，候選也照前文排。
func TestUntonedSyllableWithNeighboursUsesLanguageModel(t *testing.T) {
	for _, c := range []struct{ typed, want, first string }{
		{"ㄐㄧㄏㄠ", "幾號", "號"},
		{"ㄋㄧㄏㄠ", "你好", "好"},
	} {
		s := newLoneSession()
		typeBare(s, c.typed)
		if snap := key(s, proto.KeyEnter); snap.Commit != c.want {
			t.Errorf("%s 送出 %q，應為 %q", c.typed, snap.Commit, c.want)
		}
		s = newLoneSession()
		typeBare(s, c.typed)
		key(s, proto.KeyRight) // 完成最後一個音節，游標在句尾
		snap := s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
		if snap.Candidates == nil || snap.Candidates.Items[0].Text != c.first {
			t.Errorf("%s 最後一個字的候選 %+v，第一個應為 %q", c.typed, snap.Candidates, c.first)
		}
	}
}

// 前一句的末詞經外殼確認後是前文：單獨一個音節也照語言模型（幾｜ㄏㄠ 是號）。
func TestLoneUntonedSyllableAfterConfirmedContextUsesLanguageModel(t *testing.T) {
	s := newLoneSession()
	s.Opt.DocumentContextWeight = 1
	s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
	typeBare(s, "ㄐㄧˇ")
	snap := key(s, proto.KeyEnter)
	if snap.Commit != "幾" || snap.ContextID == 0 {
		t.Fatalf("送出 %q、前文代號 %d", snap.Commit, snap.ContextID)
	}
	s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: snap.ContextID})
	if got := loneCandidates(t, s, "ㄏㄠ"); got[0] != "號" {
		t.Fatalf("前文是「幾」時 ㄏㄠ 的候選 %v，第一個應為號", got)
	}
}

// 游標前是阿拉伯數字（#337）也是前文：句首配對正好是接在數字後面的字，照舊（3｜ㄏㄠ 是號）。
func TestLoneUntonedSyllableAfterDigitKeepsSentenceStartOrder(t *testing.T) {
	s := newLoneSession()
	s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
	typeDigitThenZhuyin(t, s, "3")
	if got := loneCandidates(t, s, "ㄏㄠ"); got[0] != "號" {
		t.Fatalf("數字後面 ㄏㄠ 的候選 %v，第一個應為號", got)
	}
}

// 標點把組字切成幾段，各段從句首算；只有一個不打聲調音節的那一段同樣照字的常用程度。
func TestLoneUntonedSyllableAfterPunctuation(t *testing.T) {
	s := newLoneSession()
	typeBare(s, "ㄋㄧㄏㄠ")
	key(s, "<") // ，
	typeBare(s, "ㄏㄠ")
	if snap := key(s, proto.KeyEnter); snap.Commit != "你好，好" {
		t.Fatalf("送出 %q", snap.Commit)
	}
}

// 選過的字照舊記得：單獨打 ㄏㄠ 選了號，下次單獨打 ㄏㄠ 就是號（個人的句首紀錄不受影響）。
func TestLoneUntonedSyllableKeepsPersonalChoice(t *testing.T) {
	s := newLoneSession()
	typeBare(s, "ㄏㄠ")
	snap := key(s, proto.KeyDown)
	id := slices.Index(texts(snap.Candidates.Items), "號")
	s.Handle(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[id].ID})
	if snap = key(s, proto.KeyEnter); snap.Commit != "號" {
		t.Fatalf("選號後送出 %q", snap.Commit)
	}
	if got := loneCandidates(t, s, "ㄏㄠ"); got[0] != "號" {
		t.Fatalf("選過號之後 ㄏㄠ 的候選 %v，第一個應為號", got)
	}
}

// 簡拼（手機）：單獨一個聲母或介音沒打聲調時同樣照字的常用程度；打了聲調的簡拼照舊。
func TestLoneAbbreviatedSyllableRanksByFrequency(t *testing.T) {
	s := newLoneSession()
	s.Abbreviation = true
	if got := loneCandidates(t, s, "ㄏ"); !slices.Equal(got[:2], []string{"好", "號"}) {
		t.Errorf("ㄏ 的候選 %v，應由好、號開頭", got)
	}
	s = newLoneSession()
	s.Abbreviation = true
	if got := loneCandidates(t, s, "ㄧ"); got[0] != "一" {
		t.Errorf("ㄧ 的候選 %v，第一個應為一", got)
	}
	s = newLoneSession()
	s.Abbreviation = true
	if got := loneCandidates(t, s, "ㄏˋ"); !slices.Equal(got[:2], []string{"號", "好"}) {
		t.Errorf("ㄏˋ 的候選 %v，應由號、好開頭", got)
	}
}

// 漢語拼音（#324）沒打聲調的音節同樣：hao 是好，hao4 照舊是號。
func TestLonePinyinSyllableRanksByFrequency(t *testing.T) {
	for _, c := range []struct{ keys, want string }{{"hao", "好"}, {"hao4", "號"}, {"yi", "一"}, {"yi4", "億"}} {
		s := newLoneSession()
		s.SetLayout(layout.Pinyin)
		for _, k := range c.keys {
			key(s, string(k))
		}
		snap := key(s, proto.KeyDown)
		if snap.Candidates == nil || snap.Candidates.Items[0].Text != c.want {
			t.Errorf("%s 的候選 %+v，第一個應為 %q", c.keys, snap.Candidates, c.want)
		}
	}
}

// 注音九宮格（按聲調鍵完成、聲調未指定）的單獨一個音節同樣照字的常用程度。
func TestLoneNineKeySyllableRanksByFrequency(t *testing.T) {
	s := newLoneSession()
	for _, g := range []string{"ㄏㄒㄠㄩ", "ㄏㄒㄠㄩ", "ˊˇˋ˙"} {
		s.Handle(proto.Request{Type: proto.Symbol, Group: g})
	}
	snap := s.Handle(proto.Request{Type: proto.Candidates})
	if snap.Candidates == nil || !slices.Equal(texts(snap.Candidates.Items)[:2], []string{"好", "號"}) {
		t.Fatalf("九宮格 ㄏㄠ 的候選 %+v，應由好、號開頭", snap.Candidates)
	}
}

// loneScorer 只拿掉系統的句首配對：語言模型沒有句首配對時，分數與原模型完全相同。
func TestLoneScorerEqualsModelWithoutSentenceStartPairs(t *testing.T) {
	s := newTonelessSession(true) // 這份詞庫沒有任何配對
	lone := &loneScorer{Model: s.model}
	for _, w := range []string{"好", "號", "你好", "沒有這個詞"} {
		if a, b := lone.Prob(lm.BOS, w), s.model.Prob(lm.BOS, w); a != b {
			t.Errorf("Prob(%s) %v，原模型 %v", w, a, b)
		}
		if a, b := lone.SystemProb(lm.BOS, w), s.model.SystemProb(lm.BOS, w); a != b {
			t.Errorf("SystemProb(%s) %v，原模型 %v", w, a, b)
		}
		if a, b := lone.PriorMass(lm.BOS, w), s.model.PriorMass(lm.BOS, w); a != b {
			t.Errorf("PriorMass(%s) %v，原模型 %v", w, a, b)
		}
	}
}
