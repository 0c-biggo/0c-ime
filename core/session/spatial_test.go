package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func spatialSession(toneless bool, weight float64) *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"哈", "ㄏㄚ", 2e-3}, {"哈哈", "ㄏㄚ ㄏㄚ", 1e-3}, {"蝦", "ㄒㄧㄚ", 5e-4},
		{"好", "ㄏㄠˇ", 5e-3}, {"小", "ㄒㄧㄠˇ", 4e-3}, {"你", "ㄋㄧˇ", 1e-2}, {"你好", "ㄋㄧˇ ㄏㄠˇ", 2e-3},
		{"漢", "ㄏㄢˋ", 1e-3}, {"兒", "ㄦˊ", 1e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.Toneless, s.Abbreviation = toneless, toneless
	s.Spatial = SpatialOptions{Weight: weight}
	return s
}

// touch types one key; near lists the neighbours the touch model gave it.
func touch(s *Session, sym string, near ...proto.NearKey) proto.Snapshot {
	return s.Handle(proto.Request{Type: proto.Symbol, Symbol: sym, Near: near})
}

func spatialCommit(s *Session) string { return s.Handle(proto.Request{Type: proto.Commit}).Commit }

// 使用者回報：哈哈 打成 ㄏㄚㄒㄚ（ㄒ 在 ㄏ 旁邊）。
func TestSpatialFixesAnEarlierKeyOfTheSyllable(t *testing.T) {
	s := spatialSession(true, 1)
	touch(s, "ㄏ")
	touch(s, "ㄚ")
	touch(s, "ㄒ", proto.NearKey{Symbol: "ㄏ", Cost: 1.2}, proto.NearKey{Symbol: "ㄖ", Cost: 3})
	touch(s, "ㄚ")
	if got := spatialCommit(s); got != "哈哈" {
		t.Fatalf("toneless: %q", got)
	}
	s = spatialSession(false, 1)
	for _, k := range []string{"ㄏ", "ㄚ", "ˉ"} {
		touch(s, k)
	}
	touch(s, "ㄒ", proto.NearKey{Symbol: "ㄏ", Cost: 1.2})
	touch(s, "ㄚ")
	touch(s, "ˉ")
	if got := spatialCommit(s); got != "哈哈" {
		t.Fatalf("toned: %q", got)
	}
}

func TestSpatialFixesTheKeyJustTyped(t *testing.T) {
	// 漢 ㄏㄢˋ：ㄢ 按成旁邊的 ㄦ，ㄏㄦ 不成音節；不改的話會拆成 ㄏ（簡拼）＋ㄦ。
	s := spatialSession(true, 1)
	touch(s, "ㄏ")
	touch(s, "ㄦ", proto.NearKey{Symbol: "ㄢ", Cost: 0.8})
	if got := spatialCommit(s); got != "漢" {
		t.Fatalf("got %q", got)
	}
}

func TestSpatialLegalWrongSyllableWinsOnContext(t *testing.T) {
	// 你好 打成 ㄋㄧˇㄒㄠˇ：ㄒㄠ 合法但沒有字；鄰鍵 ㄏ 讓整句成詞。
	s := spatialSession(false, 1)
	for _, k := range []string{"ㄋ", "ㄧ", "ˇ"} {
		touch(s, k)
	}
	touch(s, "ㄒ", proto.NearKey{Symbol: "ㄏ", Cost: 1})
	touch(s, "ㄠ")
	touch(s, "ˇ")
	if got := spatialCommit(s); got != "你好" {
		t.Fatalf("got %q", got)
	}
}

func TestSpatialOffOrWithoutNearChangesNothing(t *testing.T) {
	for _, weight := range []float64{0, 1} {
		s := spatialSession(true, weight)
		touch(s, "ㄏ")
		touch(s, "ㄚ")
		if weight == 0 {
			touch(s, "ㄒ", proto.NearKey{Symbol: "ㄏ", Cost: 1})
		} else {
			touch(s, "ㄒ")
		}
		touch(s, "ㄚ")
		if got := spatialCommit(s); got == "哈哈" {
			t.Fatalf("weight %v corrected without being asked", weight)
		}
	}
	// A syllable that stands alone is not a typo: 使用 is ㄕ＋ㄩㄥ, even with ㄨ next to ㄩ.
	s0 := spatialSession(true, 1)
	touch(s0, "ㄕ")
	touch(s0, "ㄩ", proto.NearKey{Symbol: "ㄨ", Cost: 0.5})
	touch(s0, "ㄥ")
	if len(s0.syls) != 1 || s0.comp.String() != "ㄩㄥ" {
		t.Fatalf("ㄕ＋ㄩㄥ was corrected: %q + %q", s0.syls, s0.comp.String())
	}
	// A new syllable is not a typo: ㄋㄏ stays two abbreviated syllables.
	s := spatialSession(true, 1)
	touch(s, "ㄋ")
	touch(s, "ㄏ", proto.NearKey{Symbol: "ㄒ", Cost: 1})
	if got := spatialCommit(s); got != "你好" {
		t.Fatalf("abbreviation: %q", got)
	}
}

func TestSpatialCandidatesAreLabelled(t *testing.T) {
	s := spatialSession(false, 1)
	for _, k := range []string{"ㄋ", "ㄧ", "ˇ"} {
		touch(s, k)
	}
	touch(s, "ㄒ", proto.NearKey{Symbol: "ㄏ", Cost: 1})
	touch(s, "ㄠ")
	touch(s, "ˇ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	snap := s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	labels := map[string]string{}
	for _, it := range snap.Candidates.Items {
		labels[it.Text] = it.Label
	}
	if labels["好"] != proto.CandidateCorrected || labels["小"] == proto.CandidateCorrected {
		t.Fatalf("labels %v", labels)
	}
}

// 你好 打成 ㄋㄧˇㄍㄠ（不打聲調，ㄍ 旁邊是 ㄏ；ㄍㄠ 合法但詞庫沒有字）。沒打聲調的音節換掉一鍵的讀音（鄰鍵）同打了聲調時一樣是修正，不是使用者打的音：組字區標 corrected、候選標
// corrected，Fix 也不能只靠鄰鍵對上的讀音把詞固定下來（Fits 只算打的音）。沒打聲調的展開照舊保留。
func TestSpatialUntonedAlternativesAreCorrections(t *testing.T) {
	typed := func() *Session {
		s := spatialSession(true, 1)
		for _, k := range []string{"ㄋ", "ㄧ", "ˇ"} {
			touch(s, k)
		}
		touch(s, "ㄍ", proto.NearKey{Symbol: "ㄏ", Cost: 1})
		touch(s, "ㄠ")
		s.Handle(proto.Request{Type: proto.Key, Key: "'"}) // 只切音節：ㄍㄠ 不打聲調就完成
		return s
	}
	s := typed()
	snap := s.snapshot()
	if snap.Text() != "你好" {
		t.Fatalf("text %q", snap.Text())
	}
	corrected := false
	for _, seg := range snap.Preedit {
		corrected = corrected || seg.Style == proto.StyleCorrected
	}
	if !corrected {
		t.Errorf("a neighbour-key reading is shown as corrected: %+v", snap.Preedit)
	}

	s = typed()
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	labels := map[string]string{}
	for _, it := range s.Handle(proto.Request{Type: proto.Candidates, Length: 1}).Candidates.Items {
		labels[it.Text] = it.Label
	}
	if labels["好"] != proto.CandidateCorrected {
		t.Errorf("labels %v", labels)
	}

	s = spatialSession(true, 1) // 只打 ㄍㄠ：好 只能靠鄰鍵對上
	touch(s, "ㄍ", proto.NearKey{Symbol: "ㄏ", Cost: 1})
	touch(s, "ㄠ")
	s.Handle(proto.Request{Type: proto.Key, Key: "'"})
	if snap := s.Handle(proto.Request{Type: proto.Fix, To: 0, Length: 1, Word: "好"}); len(snap.Notices) == 0 || len(s.locks) != 0 {
		t.Errorf("fix took a word only a neighbour key reaches: %+v locks %v", snap.Preedit, s.locks)
	}
}
