package session

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 三星式注音九宮格的分組（1–10 號鍵）。
var nineGroups = map[int]string{
	1: "ㄅㄉㄚ", 2: "ㄍㄐㄞㄧ", 3: "ㄓㄗㄢㄦ", 4: "ㄆㄊㄛ", 5: "ㄎㄑㄟㄨ",
	6: "ㄔㄘㄣ", 7: "ㄇㄋㄜㄝ", 8: "ㄏㄒㄠㄩ", 9: "ㄕㄙㄤㄥ", 10: "ㄈㄌㄡㄖ",
}

const nineTone = "ˊˇˋ˙"

func nineSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"你", "ㄋㄧˇ", 1e-2}, {"泥", "ㄋㄧˊ", 5e-3}, {"米", "ㄇㄧˇ", 4e-3}, {"買", "ㄇㄞˇ", 3e-3},
		{"咪", "ㄇㄧ", 2e-3}, {"妮", "ㄋㄧ", 1e-3}, {"奶", "ㄋㄞˇ", 1e-3},
		{"好", "ㄏㄠˇ", 5e-3}, {"你好", "ㄋㄧˇ ㄏㄠˇ", 2e-3},
		{"粉", "ㄈㄣˇ", 1e-3}, {"米粉", "ㄇㄧˇ ㄈㄣˇ", 1e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	return New(personal.NewModel(lex, sys))
}

// press 按九宮格的鍵；0 是聲調鍵（聲調未指定）。
func press(s *Session, keys ...int) proto.Snapshot {
	var snap proto.Snapshot
	for _, k := range keys {
		g := nineTone
		if k > 0 {
			g = nineGroups[k]
		}
		snap = s.Handle(proto.Request{Type: proto.Symbol, Group: g})
	}
	return snap
}

func TestNineKeyTypesASyllable(t *testing.T) {
	s := nineSession()
	snap := press(s, 7, 2, 0) // ㄇㄋㄜㄝ＋ㄍㄐㄞㄧ＋聲調鍵：ㄋㄧ／ㄇㄧ／ㄇㄞ／ㄋㄞ
	if snap.Text() != "你" {
		t.Fatalf("got %q", snap.Text())
	}
	r := snap.NineReadings
	if r == nil || r.At != 0 || r.Pending || r.Current != 0 || !slices.Equal(r.Readings, []string{"ㄋㄧ", "ㄇㄧ", "ㄇㄞ", "ㄋㄞ"}) {
		t.Fatalf("nine readings %+v", r)
	}
	// 候選列得到每個讀音的字，都是打的音：不標修正、沒有近音附註。
	snap = s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	var got []string
	for _, it := range snap.Candidates.Items {
		got = append(got, it.Text)
		if it.Label == proto.CandidateCorrected || it.Note != "" {
			t.Fatalf("candidate %+v marked", it)
		}
	}
	for _, w := range []string{"你", "泥", "米", "買", "咪", "妮", "奶"} {
		if !slices.Contains(got, w) {
			t.Fatalf("candidates %v lack %s", got, w)
		}
	}
}

func TestNineKeyReadingFollowsContext(t *testing.T) {
	// 同一串鍵 7 2：接 8 8（ㄏㄠ）是你好，接 10 6（ㄈㄣ）是米粉。
	s := nineSession()
	if got := press(s, 7, 2, 0, 8, 8, 0).Text(); got != "你好" {
		t.Fatalf("你好: %q", got)
	}
	s = nineSession()
	snap := press(s, 7, 2, 0, 10, 6, 0)
	if snap.Text() != "米粉" {
		t.Fatalf("米粉: %q", snap.Text())
	}
	// 挑中的讀音寫回組字：第一個音節現在是 ㄇㄧ（仍是沒打聲調），學習用的讀音是詞庫的 ㄇㄧˇ ㄈㄣˇ。
	if s.syls[0] != zhuyin.Untone("ㄇㄧ") {
		t.Fatalf("syls %v", s.syls)
	}
	if r := s.NineReadingsAt(0); r[slices.Index(s.nine[0], s.syls[0])] != "ㄇㄧ" {
		t.Fatalf("readings %v", r)
	}
	if got, ok := s.resolvedSyllables(); !ok || zhuyin.Key(got) != "ㄇㄧˇ ㄈㄣˇ" {
		t.Fatalf("resolved %v %v", got, ok)
	}
	if r := snap.NineReadings; r == nil || r.At != 1 || r.Readings[r.Current] != "ㄈㄣ" {
		t.Fatalf("last syllable %+v", r)
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "米粉" {
		t.Fatalf("commit %q", got)
	}
}

func TestNineKeyTones(t *testing.T) {
	for _, c := range []struct {
		name, tone string
		group      bool
		want       string
	}{
		{"聲調鍵：未指定", nineTone, true, "你"},
		{"指定二聲", "ˊ", false, "泥"},
		{"指定三聲（只有一個聲調的鍵）", "ˇ", true, "你"},
		{"空白：一聲", "ˉ", false, "咪"}, // 一聲只有 咪 ㄇㄧ、妮 ㄋㄧ
	} {
		s := nineSession()
		press(s, 7, 2)
		req := proto.Request{Type: proto.Symbol, Symbol: c.tone}
		if c.group {
			req = proto.Request{Type: proto.Symbol, Group: c.tone}
		}
		snap := s.Handle(req)
		if snap.Text() != c.want || !snap.Handled {
			t.Fatalf("%s: %q handled=%v", c.name, snap.Text(), snap.Handled)
		}
	}
	// 一聲時列的讀音也是一聲：ㄇㄧ、ㄋㄧ 排前面（有字），沒有一聲字的排後面。
	s := nineSession()
	press(s, 7, 2)
	s.Handle(proto.Request{Type: proto.Symbol, Symbol: "ˉ"})
	if got := s.nine[0]; got[0] != "ㄇㄧ" || got[1] != "ㄋㄧ" || got[0].Untoned() {
		t.Fatalf("tone 1 readings %v", got)
	}
	// 沒有九宮格的鍵時聲調鍵落單：不收。
	s = nineSession()
	if snap := s.Handle(proto.Request{Type: proto.Symbol, Group: nineTone}); snap.Handled || snap.Text() != "" {
		t.Fatalf("lone tone key %+v", snap)
	}
}

func TestNineKeyBackspace(t *testing.T) {
	s := nineSession()
	press(s, 7, 2)
	snap := s.Handle(proto.Request{Type: proto.Backspace})
	if r := snap.NineReadings; snap.Text() != "ㄋ" || r == nil || !r.Pending || r.Readings[0] != "ㄋ" || len(s.nineKeys) != 1 {
		t.Fatalf("one key left: %q %+v", snap.Text(), r)
	}
	snap = s.Handle(proto.Request{Type: proto.Backspace})
	if snap.Text() != "" || s.Composing() {
		t.Fatalf("all keys gone: %q", snap.Text())
	}
	if snap = s.Handle(proto.Request{Type: proto.Backspace}); snap.Handled {
		t.Fatal("backspace with nothing composing is handled")
	}
	// 完成之後同一般音節：一次刪掉整個音節，讀音一起拿掉。
	press(s, 7, 2, 0, 8, 8, 0)
	snap = s.Handle(proto.Request{Type: proto.Backspace})
	if snap.Text() != "你" || len(s.nine) != 1 || s.nine[0] == nil {
		t.Fatalf("after deleting 好: %q nine=%v", snap.Text(), s.nine)
	}
	snap = s.Handle(proto.Request{Type: proto.Backspace})
	if snap.Text() != "" || len(s.nine) != 0 {
		t.Fatalf("after deleting 你: %q nine=%v", snap.Text(), s.nine)
	}
}

func TestNineKeyFixReading(t *testing.T) {
	s := nineSession()
	press(s, 7, 2, 0)
	snap := s.Handle(proto.Request{Type: proto.NineReading, To: 0, Word: "ㄉㄚ"})
	if len(snap.Notices) == 0 || snap.Text() != "你" {
		t.Fatalf("unknown reading changed something: %+v", snap)
	}
	snap = s.Handle(proto.Request{Type: proto.NineReading, To: 0, Word: "ㄇㄧ"})
	if snap.Text() != "米" || snap.NineReadings != nil || s.NineReadingsAt(0) != nil {
		t.Fatalf("fixed: %q %+v", snap.Text(), snap.NineReadings)
	}
	// 固定之後是一般的沒打聲調音節：接 ㄏㄠ 也不會變回 你好。
	if got := press(s, 8, 8, 0).Text(); got != "米好" {
		t.Fatalf("after fixing: %q", got)
	}

	// 還沒按聲調鍵時固定那串鍵的拼法。
	s = nineSession()
	press(s, 7, 2)
	snap = s.Handle(proto.Request{Type: proto.NineReading, To: 0, Word: "ㄋㄞ"})
	if snap.Text() != "ㄋㄞ" || !slices.Equal(s.nineKeys, []string{"ㄋ", "ㄞ"}) {
		t.Fatalf("pending fixed: %q %v", snap.Text(), s.nineKeys)
	}
	if got := press(s, 0).Text(); got != "奶" {
		t.Fatalf("pending fixed then tone: %q", got)
	}
}

func TestNineKeyFixWordOnAnotherReading(t *testing.T) {
	// 觸控從清單點詞（fix）：九宮格的其他讀音也是打的音，放得進。
	s := nineSession()
	press(s, 7, 2, 0)
	snap := s.Handle(proto.Request{Type: proto.Fix, To: 0, Length: 1, Word: "買"})
	if snap.Text() != "買" || len(snap.Notices) > 0 {
		t.Fatalf("fix: %q %v", snap.Text(), snap.Notices)
	}
	if s.syls[0] != zhuyin.Untone("ㄇㄞ") {
		t.Fatalf("syls %v", s.syls)
	}
}

func TestNineKeyIgnoresKeysThatCannotStartASyllable(t *testing.T) {
	s := nineSession()
	press(s, 7, 2, 3) // ㄋㄧㄢ
	press(s, 1)       // 第四個注音：組不成任何音節開頭
	if len(s.nineKeys) != 3 {
		t.Fatalf("keys %v", s.nineKeys)
	}
}

func TestNineKeyReadingsAreCapped(t *testing.T) {
	s := nineSession()
	s.Handle(proto.Request{Type: proto.Symbol, Group: "ㄅㄆㄇㄈㄉㄊㄋㄌㄍㄎㄏㄓㄔㄕㄖㄗㄘㄙ"})
	s.Handle(proto.Request{Type: proto.Symbol, Group: "ㄚㄛㄜㄝㄞㄟㄠㄡㄢㄣㄤㄥ"})
	if n := len(nineSpellings(s.nineKeys, true)); n <= maxNineReadings {
		t.Fatalf("test needs more than %d spellings, has %d", maxNineReadings, n)
	}
	s.Handle(proto.Request{Type: proto.Symbol, Group: nineTone})
	if len(s.nine[0]) != maxNineReadings {
		t.Fatalf("kept %d readings", len(s.nine[0]))
	}
	if row := s.lattice()[0]; len(row) != 5*maxNineReadings {
		t.Fatalf("lattice row %d", len(row))
	}
}

func TestNineKeyIgnoresNear(t *testing.T) {
	s := nineSession()
	s.Spatial = SpatialOptions{Weight: 1}
	near := []proto.NearKey{{Symbol: "ㄏ", Cost: 1}}
	s.Handle(proto.Request{Type: proto.Symbol, Group: nineGroups[7], Near: near})
	s.Handle(proto.Request{Type: proto.Symbol, Group: nineGroups[2], Near: near})
	snap := s.Handle(proto.Request{Type: proto.Symbol, Group: nineTone})
	if snap.Text() != "你" || len(s.spatial) != 0 || s.compNear != nil {
		t.Fatalf("near used: %q spatial=%v", snap.Text(), s.spatial)
	}
}

func TestNineKeyLongPressSymbolIsAKey(t *testing.T) {
	// 已有九宮格的鍵時，長按選的單一注音是只有它的一顆鍵。
	s := nineSession()
	press(s, 7)
	s.Handle(proto.Request{Type: proto.Symbol, Symbol: "ㄞ"})
	if got := press(s, 0).Text(); got != "買" {
		t.Fatalf("got %q", got)
	}
}

func TestNineKeyPreviewPending(t *testing.T) {
	s := nineSession()
	press(s, 7, 2)
	c := s.PreviewPending()
	if c == nil || len(c.Items) == 0 || c.Items[0].Text != "你" {
		t.Fatalf("preview %+v", c)
	}
	if !s.ninePending() || len(s.syls) != 0 {
		t.Fatal("preview changed the session")
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "你" {
		t.Fatalf("enter finishes the keys untoned: %q", got)
	}
}

func TestNoGroupBehavesAsBefore(t *testing.T) {
	b, _ := json.Marshal(proto.Request{Type: proto.Symbol, Symbol: "ㄋ"})
	if strings.Contains(string(b), "group") || strings.Contains(string(b), "nine") {
		t.Fatalf("request %s", b)
	}
	s := nineSession()
	var snap proto.Snapshot
	for _, sym := range []string{"ㄋ", "ㄧ", "ˇ", "ㄏ", "ㄠ", "ˇ"} {
		snap = s.Handle(proto.Request{Type: proto.Symbol, Symbol: sym})
		if snap.NineReadings != nil {
			t.Fatalf("nine readings without group: %+v", snap.NineReadings)
		}
	}
	if snap.Text() != "你好" || len(s.nine) != 0 || strings.Contains(s.signature(), "nine") {
		t.Fatalf("got %q nine=%v", snap.Text(), s.nine)
	}
	if b, _ := json.Marshal(snap); strings.Contains(string(b), "nine") {
		t.Fatalf("snapshot %s", b)
	}
}

// 神經評分器不准換九宮格位置的拼法（rerank_abbrev.go）：統計首選「你好」走 ㄋㄧ，評分器偏好 ㄇㄧ 的字也換不過去；
// 送出的讀音是統計首選寫回的那個。
func TestNineKeyScorerKeepsStatisticalSpelling(t *testing.T) {
	s := nineSession()
	s.Opt.PrefixAware = false
	var seen []rerank.Request
	s.Rerank = rerank.Options{UnknownTones: true, Weight: 100, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		seen = append(seen, r)
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			if strings.HasPrefix(c.Text, "米") {
				scores[i] = 1
			}
		}
		return scores, nil
	})}
	press(s, 7, 2, 0, 8, 8, 0)
	got := s.Handle(proto.Request{Type: proto.Commit}).Commit
	if got != "你好" || len(seen) == 0 {
		t.Fatalf("got %q, %d requests", got, len(seen))
	}
	if r := seen[len(seen)-1]; zhuyin.Key(r.Reading) != "ㄋㄧ ㄏㄠ" {
		t.Fatalf("reading sent %v", r.Reading)
	}
}

// 組字條要畫九宮格音節目前的讀音：ReadingAt 是解碼挑中的那個，跟快照 nine_readings 的 current 一致，
// 固定別的讀音之後跟著變；範圍外是空字串。
func TestNineKeyReadingAt(t *testing.T) {
	s := nineSession()
	snap := press(s, 7, 2, 0, 8, 8, 0) // ㄋㄧ ㄏㄠ：你好
	if snap.Text() != "你好" {
		t.Fatalf("got %q", snap.Text())
	}
	if got := []string{s.ReadingAt(0), s.ReadingAt(1)}; !slices.Equal(got, []string{"ㄋㄧ", "ㄏㄠ"}) {
		t.Fatalf("readings %v", got)
	}
	r := snap.NineReadings
	if r == nil || r.Readings[r.Current] != s.ReadingAt(1) {
		t.Fatalf("nine readings %+v, reading %q", r, s.ReadingAt(1))
	}
	snap = s.Handle(proto.Request{Type: proto.NineReading, To: 0, Word: "ㄇㄧ"})
	if s.ReadingAt(0) != "ㄇㄧ" || !strings.HasPrefix(snap.Text(), "米") {
		t.Fatalf("after pinning ㄇㄧ: %q %q", s.ReadingAt(0), snap.Text())
	}
	if s.ReadingAt(-1) != "" || s.ReadingAt(2) != "" {
		t.Fatal("out of range")
	}
	// 一般注音鍵打的音節也照畫面寫法：打了聲調的帶聲調。
	s = nineSession()
	for _, k := range "ㄋㄧˇ" {
		s.Handle(proto.Request{Type: proto.Symbol, Symbol: string(k)})
	}
	if s.ReadingAt(0) != "ㄋㄧˇ" {
		t.Fatalf("typed reading %q", s.ReadingAt(0))
	}
}

// 被忽略的鍵（接不上任何音節開頭）與收不成音節的聲調鍵回 handled=false：外殼照 handled 記每一步，
// 不然退格會跟核心的鍵對不上（APP 的 code review 實測過：已刪的注音又跑回來）。
func TestNineKeyIgnoredKeyIsNotHandled(t *testing.T) {
	s := nineSession()
	press(s, 7, 2) // ㄇㄋㄜㄝ＋ㄍㄐㄞㄧ
	before := s.View()
	for range 3 {
		if snap := s.Handle(proto.Request{Type: proto.Symbol, Group: nineGroups[1]}); snap.Handled {
			t.Fatal("a key that starts no syllable was handled")
		}
	}
	if after := s.View(); after.Text() != before.Text() || len(s.nineKeys) != 2 {
		t.Fatalf("ignored keys changed the composition: %q → %q, %d keys", before.Text(), after.Text(), len(s.nineKeys))
	}
	// 只有聲母、組不成完整音節：聲調鍵不收。
	s = nineSession()
	s.Handle(proto.Request{Type: proto.Symbol, Symbol: "ㄐ"})
	snap := s.Handle(proto.Request{Type: proto.Symbol, Group: nineTone})
	if snap.Handled || !s.ninePending() {
		t.Fatalf("tone key on ㄐ alone: handled %v, pending %v", snap.Handled, s.ninePending())
	}
	// 長按送的單一注音接不上也一樣。
	s = nineSession()
	press(s, 7, 2)
	if snap := s.Handle(proto.Request{Type: proto.Symbol, Symbol: "ㄅ"}); snap.Handled {
		t.Fatal("a held zhuyin that starts no syllable was handled")
	}
}
