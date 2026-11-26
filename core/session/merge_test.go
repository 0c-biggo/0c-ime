package session

import (
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// mergeSession 是合併弧（merge.go）的測試詞庫：你好、好、奧、愛好……
func mergeSession(toneless bool, opt SpatialOptions) *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"你", "ㄋㄧˇ", 1e-2}, {"好", "ㄏㄠˇ", 5e-3}, {"你好", "ㄋㄧˇ ㄏㄠˇ", 2e-3},
		{"奧", "ㄠˋ", 1e-4}, {"懊悔", "ㄠˋ ㄏㄨㄟˇ", 1e-5}, {"會", "ㄏㄨㄟˋ", 2e-3},
		{"愛", "ㄞˋ", 3e-3}, {"愛好", "ㄞˋ ㄏㄠˋ", 1e-3}, {"嗨", "ㄏㄞ", 1e-4},
		{"話", "ㄏㄨㄚˋ", 2e-3}, {"電話", "ㄉㄧㄢˋ ㄏㄨㄚˋ", 2e-3}, {"電", "ㄉㄧㄢˋ", 3e-3},
		{"蝦", "ㄒㄧㄚ", 5e-4}, {"哈", "ㄏㄚ", 2e-3}, {"物", "ㄨˋ", 2e-3},
		{"後衛", "ㄏㄡˋ ㄨㄟˋ", 1e-3}, {"為", "ㄨㄟˋ", 1e-5}, {"做", "ㄗㄨㄛˋ", 3e-3}, {"作", "ㄗㄨㄛˋ", 1e-3}, {"喔", "ㄛ", 1e-4},
		{"向", "ㄒㄧㄤˋ", 3e-3}, {"日", "ㄖˋ", 1e-3}, {"樣", "ㄧㄤˋ", 1e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.Toneless, s.Abbreviation = toneless, toneless
	s.Spatial = opt
	return s
}

var mergeOn = SpatialOptions{Weight: 1, Penalty: 2, Swap: 3}

// 負責人回報的打錯順序：ㄏㄠ 打成 ㄠㄏ、聲調先打成 ㄏˇㄠ，介音晚打 ㄏㄚㄨ。合併弧把相鄰兩個打出來的音節合成一個。
func TestMergeSwapRecoversOrder(t *testing.T) {
	for _, c := range []struct{ keys, want string }{
		{"ㄠㄏ", "好"},      // 不打聲調：ㄠ（奧）＋ㄏ（簡拼）
		{"ㄋㄧˇㄠㄏˇ", "你好"}, // 打聲調：ㄠ＋ㄏˇ
		{"ㄋㄧˇㄏˇㄠˇ", ""},  // 兩個聲調：合不成一個音節
		{"ㄋㄧˇㄏˇㄠ", "你好"}, // 聲調先打：ㄏˇ（只限三聲的簡拼）＋ㄠ
		{"ㄉㄧㄢㄏㄚㄨ", "電話"}, // 介音晚打：ㄏㄚ＋ㄨ
	} {
		s := mergeSession(true, mergeOn)
		touchKeys(s, c.keys)
		got := spatialCommit(s)
		if c.want != "" && got != c.want || c.want == "" && got == "你好" {
			t.Errorf("%s: got %q, want %q", c.keys, got, c.want)
		}
		off := mergeSession(true, SpatialOptions{Weight: 1, Penalty: 2})
		touchKeys(off, c.keys)
		if got := spatialCommit(off); c.want != "" && got == c.want {
			t.Errorf("%s without merging already gave %q: the test shows nothing", c.keys, got)
		}
	}
}

// 沒打錯的不改：愛好 ㄞㄏㄠ、只打首音 ㄋㄏ、桌面（沒有觸控容錯）都跟沒有這個功能時相同。
func TestMergeLeavesTypedAlone(t *testing.T) {
	for _, keys := range []string{"ㄞㄏㄠ", "ㄋㄏ", "ㄋㄧㄏㄠ"} {
		on, off := mergeSession(true, mergeOn), mergeSession(true, SpatialOptions{Weight: 1, Penalty: 2})
		touchKeys(on, keys)
		touchKeys(off, keys)
		if a, b := on.snapshot().Text(), off.snapshot().Text(); a != b {
			t.Errorf("%s: %q, without merging %q", keys, a, b)
		}
		if a, b := spatialCommit(on), spatialCommit(off); a != b {
			t.Errorf("%s: committed %q, without merging %q", keys, a, b)
		}
	}
	desk := mergeSession(true, SpatialOptions{Swap: 3}) // Weight 0：桌面
	touchKeys(desk, "ㄠㄏ")
	if got := spatialCommit(desk); got == "好" || desk.merges != nil {
		t.Errorf("merged without the touch keyboard: %q", got)
	}
}

// 合成的位置是一個音節：畫面一個字、標成修正，候選標 corrected，TypedAt 回報打的兩個音節；退格拆回去、刪掉後面那個。
func TestMergedSyllableIsOnePosition(t *testing.T) {
	s := mergeSession(true, mergeOn)
	touchKeys(s, "ㄋㄧˇㄠㄏˇ")
	snap := s.snapshot()
	if snap.Text() != "你好" || len(s.syls) != 2 || s.syls[1] != "ㄏㄠˇ" {
		t.Fatalf("text %q syls %q", snap.Text(), s.syls)
	}
	if got := s.TypedAt(1); !slices.Equal(got, []string{"ㄠ", "ㄏˇ"}) {
		t.Errorf("typed %q", got)
	}
	if got := s.TypedAt(0); !slices.Equal(got, []string{"ㄋㄧˇ"}) {
		t.Errorf("typed %q", got)
	}
	corrected := false
	for _, seg := range snap.Preedit {
		corrected = corrected || seg.Style == proto.StyleCorrected
	}
	if !corrected {
		t.Errorf("merged reading not shown as a correction: %+v", snap.Preedit)
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	labels := map[string]string{}
	for _, it := range s.Handle(proto.Request{Type: proto.Candidates, Length: 1}).Candidates.Items {
		labels[it.Text] = it.Label
	}
	if labels["好"] != proto.CandidateCorrected {
		t.Errorf("labels %v", labels)
	}
	s.Handle(proto.Request{Type: proto.Close})
	s.Handle(proto.Request{Type: proto.Cursor, To: 2})
	snap = s.Handle(proto.Request{Type: proto.Backspace})
	if !slices.Equal(s.syls, []zhuyin.Syllable{"ㄋㄧˇ", zhuyin.Untone("ㄠ")}) || len(s.merges) != 0 {
		t.Errorf("after backspace: syls %q merges %v text %q", s.syls, s.merges, snap.Text())
	}
}

// 後面打的字讓合併不划算時拆回去：每次音節改變都在窗內重新決定。
func TestMergeIsRedecided(t *testing.T) {
	s := mergeSession(true, mergeOn)
	touchKeys(s, "ㄠㄏ")
	s.Handle(proto.Request{Type: proto.Key, Key: "'"}) // 只切音節：ㄏ 不打聲調就完成
	if len(s.merges) != 1 || s.snapshot().Text() != "好" {
		t.Fatalf("ㄠㄏ: syls %q text %q", s.syls, s.snapshot().Text())
	}
	touchKeys(s, "ㄨㄟ") // ㄏ 跟 ㄨㄟ 是後衛：合了反而只剩 好＋為
	if got := spatialCommit(s); got != "奧後衛" {
		t.Errorf("got %q", got)
	}
}

// 切成三段的也合成一個位置；退格拆回去、刪掉最後一段。
func TestMergeThreeParts(t *testing.T) {
	s := mergeSession(true, mergeOn)
	touchKeys(s, "ㄨㄗㄛ")
	s.Handle(proto.Request{Type: proto.Key, Key: "'"})
	if got := s.TypedAt(0); len(s.syls) != 1 || !slices.Equal(got, []string{"ㄨ", "ㄗ", "ㄛ"}) {
		t.Fatalf("syls %q typed %q", s.syls, got)
	}
	s.Handle(proto.Request{Type: proto.Backspace})
	if len(s.syls) != 2 || s.syls[1] != zhuyin.Abbreviate("ㄗ", 0) {
		t.Errorf("after backspace: %q", s.syls)
	}
}

// 只打首音時，還沒打完的聲母也算旁邊的簡拼（ㄨㄅ 打到 ㄍ，不把 ㄨㄅ 先合成「不」）；打了聲調的簡拼不看它，
// 打下一個字的聲母時合好的字不先拆開。
func TestMergeWaitsForPendingInitial(t *testing.T) {
	s := mergeSession(true, mergeOn)
	touchKeys(s, "ㄠㄏˇㄋ")
	if s.snapshot().Text() != "好ㄋ" {
		t.Errorf("toned: %q", s.snapshot().Text())
	}
	s = mergeSession(true, mergeOn)
	touchKeys(s, "ㄠㄏㄋ")
	if len(s.merges) != 0 {
		t.Errorf("merged before the pending initial was known: %q", s.syls)
	}
}

// 重複按了一鍵：ㄏㄏㄠ 是 ㄏ（簡拼）＋ㄏㄠ，去掉一個 ㄏ 合成 ㄏㄠ。疊字（ㄛ＋ㄛ）不合；Repeat 是 0 時不放。
func TestMergeRepeatDropsTheExtraKey(t *testing.T) {
	opt := mergeOn
	opt.Repeat = 6
	for _, c := range []struct {
		keys, want string
		opt        SpatialOptions
	}{
		{"ㄋㄧˇㄏㄏㄠˇ", "你好", opt},
		{"ㄋㄧˇㄏㄏㄠˇ", "", mergeOn},
	} {
		s := mergeSession(true, c.opt)
		touchKeys(s, c.keys)
		got := spatialCommit(s)
		if c.want != "" && got != c.want || c.want == "" && got == "你好" {
			t.Errorf("%s %+v: got %q", c.keys, c.opt, got)
		}
	}
	s := mergeSession(true, opt)
	touchKeys(s, "ㄛㄛ")
	s.Handle(proto.Request{Type: proto.Key, Key: "'"})
	if len(s.syls) != 2 || len(s.merges) != 0 {
		t.Errorf("reduplication merged: %q", s.syls)
	}
}

// 客服回報（BD-e82c3a）：想打「向」，ㄒ 按到旁邊的 ㄖ。不打聲調時 ㄖ 自己是完整音節（日），ㄧㄤ 切成下一個音節；
// 換鍵的合併弧把 ㄖ 換成鄰鍵 ㄒ、跟 ㄧㄤ 合成 ㄒㄧㄤ。沒有鄰鍵、Rejoin 是 0 時照打的音。
func TestMergeRejoinUsesTheNeighbourKey(t *testing.T) {
	opt := mergeOn
	opt.Rejoin = 3
	for _, c := range []struct {
		opt  SpatialOptions
		near bool
		want bool
	}{{opt, true, true}, {opt, false, false}, {mergeOn, true, false}} {
		s := mergeSession(true, c.opt)
		var near []proto.NearKey
		if c.near {
			near = []proto.NearKey{{Symbol: "ㄒ", Cost: 1}}
		}
		touch(s, "ㄖ", near...)
		touchKeys(s, "ㄧㄤ")
		if got := spatialCommit(s); (got == "向") != c.want {
			t.Errorf("%+v near %v: got %q", c.opt, c.near, got)
		}
	}
}

// APP#36：只打首音時「好意」是 ㄏ＋ㄧ，ㄏㄧ 接不上，但 ㄏ 是簡拼、ㄧ 是下一個音節，不是按錯 ㄒ。開著 Rejoin 時，正在連續簡拼
// 或觸控沒有明顯偏向鄰鍵（成本超過 loneSubstituteMax）就不在組音時換鍵（換了沒有成本、不經語言模型），兩個打出來的音節照打的
// 樣子放，由換鍵的合併弧比較兩種切法；真的按錯（哈 的 ㄏ 按成 ㄒ）照樣救回。觸控明顯偏向鄰鍵、或沒開 Rejoin 時照原本在組音時換。
func TestRejoinDecidesALoneAbbreviatedInitial(t *testing.T) {
	rejoin := mergeOn
	rejoin.Rejoin = 3
	far := loneSubstituteMax + 1
	for _, c := range []struct {
		opt  SpatialOptions
		lead string
		cost float64
		want string
	}{
		{rejoin, "", far, "ㄏ ㄧ"},
		{rejoin, "ㄋ", 0.5, "ㄋ ㄏ ㄧ"}, // 正在連續簡拼
		{rejoin, "", 0.5, "ㄒㄧ"},     // 觸控明顯偏向 ㄒ
		{mergeOn, "", far, "ㄒㄧ"},    // 沒開 Rejoin
	} {
		s := mergeSession(true, c.opt)
		touchKeys(s, c.lead)
		touch(s, "ㄏ", proto.NearKey{Symbol: "ㄒ", Cost: c.cost})
		touch(s, "ㄧ")
		var got []string
		for _, y := range s.syls {
			got = append(got, string(y.Base()))
		}
		if g := strings.Join(append(got, s.comp.String()), " "); g != c.want {
			t.Errorf("rejoin %v %s ㄏ(ㄒ %.1f) ㄧ: composing %q, want %q", c.opt.Rejoin, c.lead, c.cost, g, c.want)
		}
	}
	for _, keys := range []string{"ㄒㄚ", "ㄒㄚˉ", "ㄋㄧˇㄒㄚ"} {
		s := mergeSession(true, rejoin)
		for _, k := range keys {
			if k == 'ㄒ' {
				touch(s, "ㄒ", proto.NearKey{Symbol: "ㄏ", Cost: far})
			} else {
				touch(s, string(k))
			}
		}
		if got := spatialCommit(s); !strings.HasSuffix(got, "哈") {
			t.Errorf("%s: got %q", keys, got)
		}
	}
}

// APP#36：只打首音時一串只打一鍵的音節（ㄏ ㄧ ㄙ）不放換鍵的合併弧（ㄏ 按到 ㄋ 時的 ㄋㄧ）；後面接著打兩鍵以上的音節時
// （不打聲調的 ㄙㄨ）照樣放。
func TestRejoinSkipsARunOfOneKeySyllables(t *testing.T) {
	opt := mergeOn
	opt.Rejoin = 3
	rejoins := func(s *Session) int {
		w0, units := s.mergeTail()
		n := 0
		for _, c := range s.mergeCandidates(w0, splitMerged(units)) {
			if c.kind == mergeRejoin {
				n++
			}
		}
		return n
	}
	s := mergeSession(true, opt)
	touch(s, "ㄏ", proto.NearKey{Symbol: "ㄋ", Cost: loneSubstituteMax + 1})
	touchKeys(s, "ㄧㄙ")
	if n := rejoins(s); n != 0 {
		t.Errorf("ㄏ ㄧ ㄙ: %d rejoin candidates", n)
	}
	touch(s, "ㄨ")
	if n := rejoins(s); n == 0 {
		t.Errorf("ㄏ ㄧ ㄙㄨ: no rejoin candidates")
	}
}
