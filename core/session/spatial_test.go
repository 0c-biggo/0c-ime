package session

import (
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/core/input"
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

func editSession(toneless bool, missed float64) *Session {
	s := spatialSession(toneless, 1)
	s.Spatial.Penalty, s.Spatial.Missed = 2, missed
	return s
}

func touchKeys(s *Session, keys string) {
	for _, k := range keys {
		touch(s, string(k))
	}
}

// 你好 打成 ㄋㄧˇㄠˇ（ㄏ 沒按到）：ㄠˇ 沒有字，補上一個注音的讀音 ㄏㄠˇ 讓整句成詞。
func TestSpatialMissedKeyIsAdded(t *testing.T) {
	for _, missed := range []float64{0, 4} {
		s := editSession(false, missed)
		touchKeys(s, "ㄋㄧˇㄠˇ")
		got := spatialCommit(s)
		if missed > 0 && got != "你好" || missed == 0 && got == "你好" {
			t.Errorf("missed %v: %q", missed, got)
		}
	}
	// 不打聲調時 ㄋㄧㄏㄠ 漏了 ㄏ 是 ㄋㄧㄠ：組成一個合法音節（鳥），音節數跟想打的不同，不在這裡處理。
	s := editSession(true, 4)
	touchKeys(s, "ㄋㄧㄠ")
	if got := spatialCommit(s); got == "你好" {
		t.Errorf("merged syllables were split: %q", got)
	}
}

// 補上一鍵的讀音是修正：候選標 corrected；打的音本身有字時，成本讓它照舊勝出。
func TestSpatialEditsAreCorrectionsAndCostMore(t *testing.T) {
	s := editSession(false, 4)
	touchKeys(s, "ㄋㄧˇㄠˇ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	labels := map[string]string{}
	for _, it := range s.Handle(proto.Request{Type: proto.Candidates, Length: 1}).Candidates.Items {
		labels[it.Text] = it.Label
	}
	if labels["好"] != proto.CandidateCorrected {
		t.Errorf("labels %v", labels)
	}
	// 小 ㄒㄧㄠˇ 本身有字，補上一鍵的讀音都要多付成本：照打的音。
	s = editSession(false, 4)
	touchKeys(s, "ㄒㄧㄠˇ")
	if got := spatialCommit(s); got != "小" {
		t.Errorf("typed reading lost: %q", got)
	}
}

func TestSpatialEditsNeedSpatial(t *testing.T) {
	s := editSession(false, 4)
	s.Spatial.Weight = 0 // 桌面：沒開觸控容錯
	touchKeys(s, "ㄋㄧˇㄠˇ")
	if got := spatialCommit(s); got == "你好" || len(s.spatial) != 0 {
		t.Errorf("edits without spatial: %q %v", got, s.spatial)
	}
}

func TestSyllableKeyEdits(t *testing.T) {
	if got := missedKey()["ㄏㄚ"]; !slices.Contains(got, "ㄏㄨㄚ") || slices.Contains(got, "ㄏㄚ") {
		t.Errorf("ㄏㄚ missed %v", got)
	}
	if got := missedKey()["ㄒㄠ"]; !slices.Equal(got, []zhuyin.Syllable{"ㄒㄧㄠ"}) {
		t.Errorf("ㄒㄠ (illegal as typed) missed %v", got)
	}
}

// 不打聲調時漏按介音：小 打成 ㄒㄠ。ㄒ＋ㄠ 接不上，不補的話拆成 ㄒ（簡拼）＋ㄠ；補上 ㄧ 就是 ㄒㄧㄠ。
func TestSpatialMissedMedialWhileComposing(t *testing.T) {
	for _, missed := range []float64{0, 4} {
		s := editSession(true, missed)
		touchKeys(s, "ㄒㄠ")
		got := spatialCommit(s)
		if missed > 0 && got != "小" || missed == 0 && got == "小" {
			t.Errorf("missed %v: %q", missed, got)
		}
	}
	// 有接得上的鄰鍵時用鄰鍵，成本再高也一樣：ㄠ 的鄰鍵 ㄧ 讓組音成為 ㄒㄧ，不補介音。
	for _, cost := range []float64{0.5, 6} {
		s := editSession(true, 4)
		touch(s, "ㄒ")
		touch(s, "ㄠ", proto.NearKey{Symbol: "ㄧ", Cost: cost})
		if got := s.comp.String(); got != "ㄒㄧ" {
			t.Errorf("near cost %v: composing %q", cost, got)
		}
	}
}

// 候選清單的音節格：清單位置以外只留路徑走的修正讀音，清單位置上的全部留著。
func TestSpatialCandidateContextKeepsOnlyThePathCorrections(t *testing.T) {
	s := editSession(false, 4)
	touchKeys(s, "ㄋㄧˇ")
	touch(s, "ㄠ", proto.NearKey{Symbol: "ㄢ", Cost: 1}) // 鄰鍵讀音 ㄢˇ 也是修正
	touch(s, "ˇ")                                      // 路徑是 你好：位置 1 走補上 ㄏ 的 ㄏㄠˇ
	s.refresh()
	full := s.expand(s.syls)
	corrected := func(row []input.Hyp) (n int, has map[zhuyin.Syllable]bool) {
		has = map[zhuyin.Syllable]bool{}
		for _, h := range row {
			if h.Corrected {
				n++
				has[h.Syl] = true
			}
		}
		return n, has
	}
	if n, _ := corrected(full[1]); n < 2 {
		t.Fatalf("position 1 has %d corrected readings", n)
	}
	lat := s.pathCorrections(s.expand(s.syls), 0, 1) // 清單在位置 0
	if n, has := corrected(lat[1]); n != 1 || !has["ㄏㄠˇ"] {
		t.Errorf("outside the list: %v", lat[1])
	}
	lat = s.pathCorrections(s.expand(s.syls), 1, 2)
	if n, _ := corrected(lat[1]); n != len(full[1])-1 {
		t.Errorf("inside the list: %d of %d", n, len(full[1]))
	}
}

// 補上一鍵的讀音只放在最後 missedWindow 個音節：更前面的只留路徑走的，路徑沒走的就不再放。
func TestSpatialMissedReadingsOnlyNearTheEnd(t *testing.T) {
	s := editSession(false, 4)
	s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: "襖", Reading: zhuyin.ParseReading("ㄠˇ"), PRW: 1})
	s.model.LM.(*lm.Mem).SetUni("襖", 1e-3)
	touchKeys(s, "ㄠˇ")    // 襖：打的音有字，路徑照打的音
	touchKeys(s, "ㄋㄧˇㄠˇ") // 你好：位置 2 走補上 ㄏ 的 ㄏㄠˇ
	for range missedWindow {
		touchKeys(s, "ㄠˇ")
	}
	if got := []rune(s.snapshot().Text()); string(got[:3]) != "襖你好" {
		t.Fatalf("text %q", string(got))
	}
	lat := s.expand(s.syls)
	count := func(at int) (n int) {
		for _, h := range lat[at] {
			if h.Corrected {
				n++
			}
		}
		return n
	}
	if n := count(2); n != 1 {
		t.Errorf("the path's correction outside the window: %d corrected readings", n)
	}
	if n := count(0); n != 0 {
		t.Errorf("a syllable the path took as typed keeps %d corrected readings", n)
	}
	if n := count(len(s.syls) - 1); n == 0 {
		t.Errorf("the last syllable has no missed-key readings")
	}
}
