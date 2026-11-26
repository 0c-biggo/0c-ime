package session

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 自動切字（nine_segment.go）：九宮格的鍵連續按、不必按聲調鍵。

func segSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"你", "ㄋㄧˇ", 1e-2}, {"泥", "ㄋㄧˊ", 5e-3}, {"米", "ㄇㄧˇ", 4e-3}, {"買", "ㄇㄞˇ", 3e-3},
		{"咪", "ㄇㄧ", 2e-3}, {"妮", "ㄋㄧ", 1e-3}, {"奶", "ㄋㄞˇ", 1e-3},
		{"好", "ㄏㄠˇ", 5e-3}, {"你好", "ㄋㄧˇ ㄏㄠˇ", 2e-3},
		{"粉", "ㄈㄣˇ", 1e-3}, {"米粉", "ㄇㄧˇ ㄈㄣˇ", 1e-3},
		{"先", "ㄒㄧㄢ", 4e-3}, {"鮮", "ㄒㄧㄢ", 1e-3}, {"西", "ㄒㄧ", 3e-3}, {"安", "ㄢ", 2e-3}, {"西安", "ㄒㄧ ㄢ", 1e-3},
		{"市", "ㄕˋ", 2e-3}, {"西安市", "ㄒㄧ ㄢ ㄕˋ", 5e-4}, {"去", "ㄑㄩˋ", 3e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.NineSegment = true
	return s
}

// keysOf 是讀音在三星式九宮格上的鍵（nineGroups 的號碼）。
func keysOf(reading string) []int {
	var out []int
	for _, y := range zhuyin.ParseReading(reading) {
		for _, r := range string(y.Base()) {
			for k := 1; k <= 10; k++ {
				if strings.ContainsRune(nineGroups[k], r) {
					out = append(out, k)
				}
			}
		}
	}
	return out
}

// segs 是目前的切法：每個九宮格音節的鍵數（固定的加 *、按了聲調鍵的加 '）。
func segs(s *Session) string {
	var b []string
	for p := range s.syls {
		n, ok := s.nineSeg[p]
		if !ok {
			b = append(b, "-")
			continue
		}
		t := fmt.Sprint(len(n.keys))
		if n.anchor >= 0 {
			t += "'"
		}
		if n.fixed != "" {
			t += "*"
		}
		b = append(b, t)
	}
	return strings.Join(b, " ")
}

func TestNineSegmentSplitsWithoutToneKey(t *testing.T) {
	s := segSession()
	var snap proto.Snapshot
	for _, k := range keysOf("ㄋㄧ ㄏㄠ") {
		snap = press(s, k)
		if !snap.Handled || snap.Text() == "" {
			t.Fatalf("key %d: %+v", k, snap)
		}
	}
	if snap.Text() != "你好" || segs(s) != "2 2" {
		t.Fatalf("got %q, segments %s", snap.Text(), segs(s))
	}
	// 打字途中尾端可以是還沒打完的音節：只按了 ㄋ 那顆鍵也顯示得出字。
	s = segSession()
	if got := press(s, 7).Text(); got == "" {
		t.Fatal("one key shows nothing")
	}
	if !s.nineSegPartial(0) {
		t.Fatal("a lone ㄇㄋㄜㄝ key should be an unfinished syllable")
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got == "" {
		t.Fatal("commit sends nothing")
	}
}

// 聲調鍵是錨點：音節在那裡結束。前面的鍵要整串切得成完整音節才收：三星式每顆鍵都有能自己成音節的韻母，
// 只有長按選的單一聲母（ㄅ）這種鍵切不成。7 2 之後按聲調鍵、再按 8 8：切成 你｜好。
func TestNineSegmentAnchor(t *testing.T) {
	s := segSession()
	press(s, 7)
	s.Handle(proto.Request{Type: proto.Symbol, Group: "ㄅ"})
	if snap := press(s, 0); snap.Handled {
		t.Fatalf("anchor after ㄇㄋㄜㄝ＋ㄅ accepted: %s", segs(s))
	}
	s = segSession()
	snap := press(s, 7, 2, 0, 8, 8)
	if snap.Text() != "你好" || segs(s) != "2' 2" {
		t.Fatalf("got %q %s", snap.Text(), segs(s))
	}
	// 指定三聲：第一個音節是 ㄋㄧˇ／ㄇㄧˇ 那些三聲的讀音。
	s = segSession()
	press(s, 7, 2)
	s.Handle(proto.Request{Type: proto.Symbol, Group: "ˇ"})
	if s.syls[0].ToneOf() != 3 {
		t.Fatalf("tone %v", s.syls)
	}
}

// 每個音節都按聲調鍵（關閉時的打法）：兩個聲調鍵之間的 ㄕㄢ 組得成一個音節，就是一個音節，不會切成 ㄕ＋ㄓ
// 兩個單一注音的音節。不按聲調鍵時照解碼切。
func TestNineSegmentToneKeyBetweenAnchorsIsOneSyllable(t *testing.T) {
	s := segSession()
	press(s, keysOf("ㄒㄧ")...)
	press(s, 0)
	press(s, keysOf("ㄕㄢ")...)
	press(s, 0)
	if got := segs(s); got != "2' 2'" {
		t.Fatalf("segments %s", got)
	}
	// 中間不按：ㄒㄧㄕㄢ 四顆鍵由解碼切，左欄照樣可以改成別的鍵數。
	s = segSession()
	snap := press(s, keysOf("ㄒㄧ ㄕㄢ")...)
	if r := snap.NineReadings; r == nil || len(r.Keys) == 0 {
		t.Fatalf("side column %+v", r)
	}
}

// 退格一次刪一顆鍵；後面按了聲調鍵時先刪聲調鍵。
func TestNineSegmentBackspaceByKey(t *testing.T) {
	s := segSession()
	press(s, 7, 2, 0, 8, 8)
	for _, want := range []string{"2' 1", "2'", "2", "1", ""} {
		s.Handle(proto.Request{Type: proto.Backspace})
		if got := segs(s); got != want {
			t.Fatalf("after backspace %q, want %q", got, want)
		}
	}
	if snap := s.Handle(proto.Request{Type: proto.Backspace}); snap.Handled {
		t.Fatal("backspace with nothing composing is handled")
	}
}

// 左欄：列從左邊數來第一個還沒固定的音節，可以選不同鍵數的拼法；選了就固定，後面的鍵重新切。
func TestNineSegmentChangeCutFromSideColumn(t *testing.T) {
	s := segSession()
	snap := press(s, keysOf("ㄒㄧㄢ")...)
	if snap.Text() != "先" || segs(s) != "3" {
		t.Fatalf("got %q %s", snap.Text(), segs(s))
	}
	r := snap.NineReadings
	if r == nil || r.At != 0 || !slices.Contains(r.Readings, "ㄒㄧ") || r.Readings[r.Current] != "ㄒㄧㄢ" {
		t.Fatalf("side column %+v", r)
	}
	snap = s.Handle(proto.Request{Type: proto.NineReading, To: 0, Word: "ㄒㄧ", Length: 2})
	if snap.Text() != "西安" || segs(s) != "2* 1" || len(snap.Notices) > 0 {
		t.Fatalf("after picking ㄒㄧ: %q %s %v", snap.Text(), segs(s), snap.Notices)
	}
	// 左欄移到下一個還沒固定的音節（ㄢ）。
	if r := snap.NineReadings; r == nil || r.At != 1 {
		t.Fatalf("side column moved to %+v", r)
	}
	// 拼法跟鍵數對不上：不改。
	if snap = s.Handle(proto.Request{Type: proto.NineReading, To: 1, Word: "ㄒㄧ", Length: 1}); len(snap.Notices) == 0 || segs(s) != "2* 1" {
		t.Fatalf("mismatch accepted: %s %v", segs(s), snap.Notices)
	}
	// 全部固定時沒有左欄。
	if snap = s.Handle(proto.Request{Type: proto.NineReading, To: 1, Word: "ㄢ"}); snap.NineReadings != nil || segs(s) != "2* 1*" {
		t.Fatalf("all fixed: %s %+v", segs(s), snap.NineReadings)
	}
}

// 候選列得到不同切法的詞；選了就照那個詞的讀音切開、固定，後面再打的鍵不會把它切開。
func TestNineSegmentSelectFixesCut(t *testing.T) {
	s := segSession()
	press(s, keysOf("ㄒㄧㄢ")...)
	snap := s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	id := -1
	for _, it := range snap.Candidates.Items {
		if it.Text == "西安" {
			id = it.ID
		}
	}
	if id < 0 {
		t.Fatalf("candidates %+v lack 西安", snap.Candidates.Items)
	}
	snap = s.Handle(proto.Request{Type: proto.Select, Index: id})
	if snap.Text() != "西安" || segs(s) != "2* 1*" {
		t.Fatalf("after select: %q %s", snap.Text(), segs(s))
	}
	snap = press(s, keysOf("ㄕ")...)
	if !strings.HasPrefix(snap.Text(), "西安") || !strings.HasPrefix(segs(s), "2* 1*") {
		t.Fatalf("after another key: %q %s", snap.Text(), segs(s))
	}
	// fix 也一樣：從句首固定「西安」（兩個字放進目前一個音節的三顆鍵）。
	s = segSession()
	press(s, keysOf("ㄒㄧㄢ")...)
	snap = s.Handle(proto.Request{Type: proto.Fix, To: 0, Length: 1, Word: "西安"})
	if snap.Text() != "西安" || segs(s) != "2* 1*" || len(snap.Notices) > 0 {
		t.Fatalf("after fix: %q %s %v", snap.Text(), segs(s), snap.Notices)
	}
	// 放不進的詞：什麼都不改。
	if snap = s.Handle(proto.Request{Type: proto.Fix, To: 0, Length: 2, Word: "你好"}); len(snap.Notices) == 0 || snap.Text() != "西安" {
		t.Fatalf("bad fix: %q %v", snap.Text(), snap.Notices)
	}
}

// 游標移到 run 中間時，游標是切點：打的鍵接在游標處，不會跟後面的鍵合成一個音節。
func TestNineSegmentTypesAtCursor(t *testing.T) {
	s := segSession()
	press(s, keysOf("ㄋㄧ ㄏㄠ")...)
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	press(s, keysOf("ㄏㄠ")...)
	if got := segs(s); got != "2 2 2" || s.cursor != 2 {
		t.Fatalf("segments %s cursor %d", got, s.cursor)
	}
	var keys []string
	for p := range s.syls {
		keys = append(keys, s.nineSeg[p].keys...)
	}
	want := []string{nineGroups[7], nineGroups[2], nineGroups[8], nineGroups[8], nineGroups[8], nineGroups[8]}
	if !slices.Equal(keys, want) {
		t.Fatalf("keys %v", keys)
	}
}

// 被換掉的詞記成拒絕（學習用），位置要跟字數一致：學習照 Syls[Start:End] 取讀音。選字或 fix 沒改音節數時照常記；
// 改了音節數（ㄒㄧㄢ 的「先」換成兩個音節的「西安」）時，「先」在新的切法裡沒有一個音節對得上，不記。
func TestNineSegmentSelectKeepsRejected(t *testing.T) {
	pick := func(word string) []personal.RejectedSeg {
		s := segSession()
		press(s, keysOf("ㄒㄧㄢ ㄋㄧ")...)
		snap := s.Handle(proto.Request{Type: proto.Cursor, To: 0})
		snap = s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
		for _, it := range snap.Candidates.Items {
			if it.Text == word {
				s.Handle(proto.Request{Type: proto.Select, Index: it.ID})
				return s.rejected
			}
		}
		t.Fatalf("no candidate %s", word)
		return nil
	}
	if got := pick("鮮"); !reflect.DeepEqual(got, []personal.RejectedSeg{{Word: "先", Start: 0, End: 1}}) {
		t.Fatalf("same cut: %+v", got)
	}
	if got := pick("西安"); len(got) != 0 {
		t.Fatalf("new cut: %+v", got)
	}
	s := segSession()
	press(s, keysOf("ㄒㄧㄢ")...)
	s.Handle(proto.Request{Type: proto.Fix, To: 0, Length: 1, Word: "西安"})
	if len(s.rejected) != 0 {
		t.Fatalf("fix: %+v", s.rejected)
	}
}

// reshown：選字改了音節數之後，畫面上的路徑換到新的位置時每一段的字數都等於它佔的音節數、首尾相接（selection
// stability 才用得上），範圍裡的自動修正拿掉（不會剩下寬度 0 的紀錄）。
func TestNineSegmentReshown(t *testing.T) {
	path := []decode.Seg{{Word: "你", Start: 0, End: 1}, {Word: "先生", Start: 1, End: 3}, {Word: "好", Start: 3, End: 4}}
	auto := []AutoChange{{Start: 0, End: 1, Text: "你"}, {Start: 2, End: 3, Text: "生"}, {Start: 3, End: 4, Text: "好"}}
	// 第 1 個音節（「先」）換成兩個音節的「西安」。
	got, kept, rejected := reshown(path, auto, 1, 1, 2, "西安")
	want := []decode.Seg{{Word: "你", Start: 0, End: 1}, {Word: "西安", Start: 1, End: 3}, {Word: "生", Start: 3, End: 4, Partial: true}, {Word: "好", Start: 4, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("path %+v", got)
	}
	if len(kept) != 3 || kept[1].Start != 3 || kept[1].End != 4 || kept[2].Start != 4 {
		t.Fatalf("auto %+v", kept)
	}
	if len(rejected) != 0 {
		t.Fatalf("rejected %+v", rejected) // 「先生」換過去佔三個音節，兩個字：不記
	}
	// 音節數沒變時照常記。
	if _, _, rejected = reshown(path, auto, 1, 1, 1, "仙"); !reflect.DeepEqual(rejected, []personal.RejectedSeg{{Word: "先生", Start: 1, End: 3}}) {
		t.Fatalf("same count: %+v", rejected)
	}
}

// 游標在一串鍵中間時打一顆自己組不成音節的鍵（長按選的 ㄈ）：游標前那幾顆鍵同尾端，展開成以它開頭的音節，
// 其他音節的切法不動，組字照樣解得出字（之前整段解不出，所有鍵退回貪婪切法，畫面變成一串注音）。
func TestNineSegmentUnfinishedKeyAtCursor(t *testing.T) {
	for _, req := range []proto.Request{{Type: proto.Symbol, Group: "ㄈ"}, {Type: proto.Symbol, Symbol: "ㄈ"}} {
		s := segSession()
		snap := press(s, keysOf("ㄒㄧ ㄢ ㄋㄧ ㄏㄠ")...)
		if snap.Text() != "先你好" || segs(s) != "3 2 2" {
			t.Fatalf("got %q %s", snap.Text(), segs(s))
		}
		s.Handle(proto.Request{Type: proto.Cursor, To: 1})
		snap = s.Handle(req)
		if got := segs(s); got != "3 1 2 2" || s.cursor != 2 {
			t.Fatalf("%+v: segments %s cursor %d", req, got, s.cursor)
		}
		if text := snap.Text(); !strings.HasPrefix(text, "先") || !strings.HasSuffix(text, "你好") || strings.ContainsAny(text, "ㄈㄒㄋㄏ") {
			t.Fatalf("%+v: text %q", req, text)
		}
		if g := snap.NineSegments[1]; !g.Partial || g.Reading != "ㄈ" {
			t.Fatalf("%+v: segment %+v", req, g)
		}
	}
}

// 游標前那顆組不成音節的鍵在按鍵圖上一律展開（不只在游標切點）：候選列得到畫面上的字、fix 得了；游標移開之後
// 整段照樣解得出來，再打的鍵照常重新切，不走解不出時的退路。展開只限那一顆鍵，前一個音節不會被切開。
func TestNineSegmentUnfinishedKeyEverywhere(t *testing.T) {
	s := segSession()
	press(s, keysOf("ㄒㄧ ㄢ ㄋㄧ ㄏㄠ")...)
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	snap := s.Handle(proto.Request{Type: proto.Symbol, Group: "ㄈ"})
	if snap.Text() != "先粉你好" || segs(s) != "3 1 2 2" {
		t.Fatalf("got %q %s", snap.Text(), segs(s))
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	snap = s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	listed := false
	for _, it := range snap.Candidates.Items {
		listed = listed || it.Text == "粉"
	}
	if !listed {
		t.Fatalf("candidates %+v lack 粉", snap.Candidates.Items)
	}
	s.Handle(proto.Request{Type: proto.Close})
	if snap = s.Handle(proto.Request{Type: proto.Fix, To: 1, Length: 1, Word: "粉"}); len(snap.Notices) > 0 || snap.Text() != "先粉你好" {
		t.Fatalf("fix 粉: %q %v", snap.Text(), snap.Notices)
	}
	// 游標移到最後：按鍵圖解得出路徑，接著打的鍵照常切。
	s = segSession()
	press(s, keysOf("ㄒㄧ ㄢ ㄋㄧ ㄏㄠ")...)
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	s.Handle(proto.Request{Type: proto.Symbol, Group: "ㄈ"})
	s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEnd})
	if !s.nineGraphDecodes() {
		t.Fatal("key graph has no path after the caret moved away")
	}
	snap = press(s, keysOf("ㄕ")...)
	if !strings.HasPrefix(snap.Text(), "先粉你好") || segs(s) != "3 1 2 2 1" {
		t.Fatalf("after another key: %q %s", snap.Text(), segs(s))
	}
}

// 解不出路徑時的退路（localCuts）：run 中間只能當開頭拼法的音節（長按的 ㄈ 加上 ㄍㄐㄞㄧ 鍵，只有 ㄈㄧㄠ 的開頭）
// 在按鍵圖上沒有讀音，要切開；之前照尾端算，當成有讀音整個留著，之後每一鍵都解不出、一直走退路。
// run 最後、沒按聲調鍵的同一個音節是還沒打完的尾端，留著。
func TestNineSegmentLocalCutsSplitsPrefixOnly(t *testing.T) {
	s := segSession()
	press(s, keysOf("ㄋㄧ ㄏㄠ")...)
	ni := slices.Clone(s.nineSeg[0].keys)
	prefix := nineSyl{keys: []string{"ㄈ", nineGroups[keysOf("ㄧ")[0]]}, anchor: -1}
	s.replaceRun(0, len(s.syls), []nineSyl{prefix, {keys: ni, anchor: -1}})
	s.syncNine()
	if got := s.localCuts(0, len(s.syls), -1); !slices.Equal(got, []int{1, 2, 4}) {
		t.Fatalf("mid-run prefix: cuts %v", got)
	}
	s.replaceRun(0, len(s.syls), []nineSyl{{keys: ni, anchor: -1}, prefix})
	s.syncNine()
	if got := s.localCuts(0, len(s.syls), -1); !slices.Equal(got, []int{2, 4}) {
		t.Fatalf("tail prefix: cuts %v", got)
	}
}

// 存檔還原：放不進的 fix 把鍵、錨點與固定的狀態整個還原。
func TestNineSegmentRestoreKeepsKeys(t *testing.T) {
	s := segSession()
	press(s, 7, 2, 0, 8, 8)
	before := segState(s)
	s.Handle(proto.Request{Type: proto.Fix, To: 0, Length: 2, Word: "西安"})
	if after := segState(s); !reflect.DeepEqual(before, after) {
		t.Fatalf("before %v after %v", before, after)
	}
	saved := s.saveSyllables()
	s.Handle(proto.Request{Type: proto.Backspace})
	s.restoreSyllables(saved)
	if after := segState(s); !reflect.DeepEqual(before, after) {
		t.Fatalf("restore: before %v after %v", before, after)
	}
}

func segState(s *Session) []string {
	var out []string
	for p := range s.syls {
		n := s.nineSeg[p]
		out = append(out, fmt.Sprintf("%s/%v/%d/%s", s.syls[p], n.keys, n.anchor, n.fixed))
	}
	return out
}

// 重播決定性：同一串請求從空組字重播，每一步的快照（切法、固定、左欄）逐一相同。
func TestNineSegmentReplayIsDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for round := range 30 {
		var reqs []proto.Request
		for range 20 {
			switch rng.Intn(10) {
			case 0:
				reqs = append(reqs, proto.Request{Type: proto.Symbol, Group: nineTone})
			case 1:
				reqs = append(reqs, proto.Request{Type: proto.Backspace})
			case 2:
				reqs = append(reqs, proto.Request{Type: proto.NineReading, To: rng.Intn(3), Word: []string{"ㄒㄧ", "ㄋㄧ", "ㄏㄠ", "ㄢ"}[rng.Intn(4)], Length: rng.Intn(3)})
			case 3:
				reqs = append(reqs, proto.Request{Type: proto.Fix, To: 0, Length: 1 + rng.Intn(2), Word: []string{"西安", "你好", "先"}[rng.Intn(3)]})
			default:
				reqs = append(reqs, proto.Request{Type: proto.Symbol, Group: nineGroups[[]int{7, 2, 8, 3, 9, 10, 6}[rng.Intn(7)]]})
			}
		}
		// 第二次每個請求前清掉逐鍵增量的狀態：結果也不能跟快取有關。
		run := func(fresh bool) []string {
			s := segSession()
			var out []string
			for i, r := range reqs {
				if fresh {
					s.nineSearch, s.sylSearch = nil, nil
				}
				if i%5 == 4 {
					// 照快照點左欄或選字，跟外殼一樣
					if r := s.View().NineReadings; r != nil && len(r.Readings) > 1 {
						k := i % len(r.Readings)
						length := 0
						if r.Keys != nil {
							length = r.Keys[k]
						}
						s.Handle(proto.Request{Type: proto.NineReading, To: r.At, Word: r.Readings[k], Length: length})
					}
				}
				snap := s.Handle(r)
				out = append(out, fmt.Sprintf("%q %v %+v %+v %s", snap.Text(), snap.Notices, snap.NineReadings, snap.NineSegments, segs(s)))
			}
			return out
		}
		a, b := run(false), run(true)
		if !slices.Equal(a, b) {
			t.Fatalf("round %d differs:\n%v\n%v", round, a, b)
		}
	}
}
