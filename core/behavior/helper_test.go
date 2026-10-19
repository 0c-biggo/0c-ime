// Package behavior 提供跨模組的行為回歸，經由協定請求操作引擎。
package behavior

import (
	"os"
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/engine"
	"git.biggo.com/0c/ime/core/layout"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
)

const toy = "../../testdata/toy/"

// useReference 為真時改用參考實作（lexicon.Mem、lm.Mem），見 TestReferenceImplementation。
var useReference bool

// user 模擬一位使用者：只透過協定請求與引擎互動。
type user struct {
	tb   testing.TB
	e    *engine.Engine
	id   uint64
	keys int // 累計按鍵數
}

type extra struct {
	surface, reading string
	prw, uni         float64
}

func newUser(tb testing.TB, p personal.Params, extras ...extra) *user {
	tb.Helper()
	lf, err := os.Open(toy + "lexicon.tsv")
	if err != nil {
		tb.Fatal(err)
	}
	defer lf.Close()
	lex, err := lexicon.Load(lf)
	if err != nil {
		tb.Fatal(err)
	}
	mf, err := os.Open(toy + "lm.tsv")
	if err != nil {
		tb.Fatal(err)
	}
	defer mf.Close()
	sys, err := lm.Load(mf)
	if err != nil {
		tb.Fatal(err)
	}
	for _, x := range extras {
		lex.Add(lexicon.Entry{Surface: x.surface, Reading: zhuyin.ParseReading(x.reading), PRW: x.prw})
		sys.SetUni(x.surface, x.uni)
	}
	var e *engine.Engine
	if useReference {
		e = engine.New(lex, sys)
	} else {
		// 預設走與正式資料包相同的路徑：建成緊湊格式、編碼、再解碼
		var es []lexicon.Entry
		lex.Walk(func(x lexicon.Entry) { es = append(es, x) })
		built, err := lexicon.Build(es)
		if err != nil {
			tb.Fatal(err)
		}
		clex, err := lexicon.Decode(built.Encode())
		if err != nil {
			tb.Fatal(err)
		}
		lmb, err := lm.BuildCompact(sys.Entries())
		if err != nil {
			tb.Fatal(err)
		}
		clm, err := lm.DecodeCompact(lmb.Encode())
		if err != nil {
			tb.Fatal(err)
		}
		e = engine.New(clex, clm)
	}
	e.Model.P = p
	return &user{tb: tb, e: e, id: e.OpenSession()}
}

func (u *user) do(r proto.Request) proto.Snapshot {
	u.tb.Helper()
	snap, err := u.e.Do(u.id, r)
	if err != nil {
		u.tb.Fatal(err)
	}
	return snap
}

// typeSyl 以標準排列的實際按鍵打出一個音節。
func (u *user) typeSyl(syl string) proto.Snapshot {
	var snap proto.Snapshot
	for _, k := range layout.Standard.Keys(zhuyin.Syllable(syl)) {
		snap = u.do(proto.Request{Type: proto.Key, Key: string(k)})
		u.keys++
	}
	return snap
}

func (u *user) typeAll(reading string) proto.Snapshot {
	var snap proto.Snapshot
	for _, syl := range strings.Fields(reading) {
		snap = u.typeSyl(syl)
	}
	return snap
}

// show 打完後看自動轉換的結果，然後放棄（不學習）。
func (u *user) show(reading string) string {
	text := u.typeAll(reading).Text()
	u.do(proto.Request{Type: proto.Cancel})
	return text
}

// singles 回傳某位置的全部單字候選（跨頁）。
func (u *user) singles(pos int) []proto.CandidateItem {
	u.do(proto.Request{Type: proto.Cursor, To: pos})
	snap := u.do(proto.Request{Type: proto.Candidates, Length: 1})
	if snap.Candidates == nil {
		return nil
	}
	items := snap.Candidates.Items
	for pg := 1; pg < snap.Candidates.Pages; pg++ {
		items = append(items, u.do(proto.Request{Type: proto.Page, Index: pg}).Candidates.Items...)
	}
	return items
}

func texts(items []proto.CandidateItem) string {
	out := ""
	for _, it := range items {
		out += it.Text
	}
	return out
}

// fix 把組字區逐字手動修正成 want，回傳手動選字次數。
func (u *user) fix(snap proto.Snapshot, want string) int {
	u.tb.Helper()
	w, n := []rune(want), 0
	for snap.Text() != want {
		got := []rune(snap.Text())
		pos := 0
		for pos < len(w) && got[pos] == w[pos] {
			pos++
		}
		id := -1
		for _, it := range u.singles(pos) {
			if it.Text == string(w[pos]) {
				id = it.ID
			}
		}
		if id < 0 {
			u.tb.Fatalf("位置 %d 的候選沒有 %q", pos, string(w[pos]))
		}
		snap = u.do(proto.Request{Type: proto.Select, Index: id})
		n++
	}
	return n
}

// typeAndFix 模擬「打字 → 看自動轉換 → 逐字修正 → 提交」。回傳修正前的文字與手動次數。
func (u *user) typeAndFix(reading, want string) (before string, manual int) {
	u.tb.Helper()
	snap := u.typeAll(reading)
	before = snap.Text()
	manual = u.fix(snap, want)
	if got := u.do(proto.Request{Type: proto.Commit}).Commit; got != want {
		u.tb.Fatalf("提交 %q 應為 %q", got, want)
	}
	return
}

// 常用讀音
const (
	QZ   = "ㄑㄧㄣ ㄗˇ"
	J    = "ㄐㄧㄥˇ"
	D    = "ㄉㄨㄥˋ"
	QZJ  = QZ + " " + J
	WXC  = "ㄨㄛˇ ㄒㄧㄤˇ ㄔ"
	YG   = "ㄧㄣ ㄍㄞ"
	MPDF = "ㄇㄚˊ ㄆㄛˊ ㄉㄡˋ ㄈㄨˇ"
)
