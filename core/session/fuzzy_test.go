package session

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func allPairs() fuzzy.Config {
	var c fuzzy.Config
	for _, p := range fuzzy.Pairs() {
		c = c.With(p, fuzzy.Select)
	}
	return c
}

type word struct {
	w, r string
	p    float64
}

// 每個目標詞都以詞庫的正常讀音列出（issue #45 的規格：缺詞另算詞庫缺口，不算規則失效）。
var fuzzyWords = []word{
	{"知識", "ㄓ ㄕˋ", 1e-3}, {"知", "ㄓ", 2e-3}, {"是", "ㄕˋ", 5e-3}, {"資", "ㄗ", 1e-3},
	{"之後", "ㄓ ㄏㄡˋ", 1e-3}, {"後", "ㄏㄡˋ", 3e-3},
	{"子女", "ㄗˇ ㄋㄩˇ", 1e-3}, {"自己", "ㄗˋ ㄐㄧˇ", 1e-3}, {"己", "ㄐㄧˇ", 1e-3},
	{"吃飯", "ㄔ ㄈㄢˋ", 1e-3}, {"吃", "ㄔ", 2e-3}, {"飯", "ㄈㄢˋ", 2e-3}, {"出門", "ㄔㄨ ㄇㄣˊ", 1e-3},
	{"材料", "ㄘㄞˊ ㄌㄧㄠˋ", 1e-3}, {"草莓", "ㄘㄠˇ ㄇㄟˊ", 1e-3},
	{"生活", "ㄕㄥ ㄏㄨㄛˊ", 1e-3}, {"身體", "ㄕㄣ ㄊㄧˇ", 1e-3},
	{"三天", "ㄙㄢ ㄊㄧㄢ", 1e-3}, {"送信", "ㄙㄨㄥˋ ㄒㄧㄣˋ", 1e-3},
	{"朋友", "ㄆㄥˊ ㄧㄡˇ", 1e-3}, {"因為", "ㄧㄣ ㄨㄟˋ", 2e-3}, {"因", "ㄧㄣ", 8e-4}, {"應", "ㄧㄥ", 4e-4},
	{"應該", "ㄧㄥ ㄍㄞ", 2e-3}, {"本來", "ㄅㄣˇ ㄌㄞˊ", 1e-3},
	{"星星", "ㄒㄧㄥ ㄒㄧㄥ", 1e-3}, {"長城", "ㄔㄤˊ ㄔㄥˊ", 1e-3}, {"聲音", "ㄕㄥ ㄧㄣ", 1e-3},
	{"我", "ㄨㄛˇ", 1e-2}, {"爸", "ㄅㄚˋ", 1e-3},
}

func fuzzyLexicon(extra ...word) (*lexicon.Mem, *lm.Mem) {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range append(slices.Clone(fuzzyWords), extra...) {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	return lex, sys
}

func fuzzySession(cfg fuzzy.Config, extra ...word) *Session {
	lex, sys := fuzzyLexicon(extra...)
	s := New(personal.NewModel(lex, sys))
	s.Fuzzy = cfg
	return s
}

// fillers 為輸入的每個合法音節補一個專屬單字：真實詞庫每個合法音節都有字，沒有字的音節會被輸入端
// 當成按錯鍵還原（見 symbol）。不在音節表的音節不補——那正是「只能靠近音成詞」的情形。
func fillers(typed string) []word {
	var out []word
	for i, y := range strings.Fields(typed) {
		if zhuyin.Syllable(y).Legal() {
			out = append(out, word{string(rune(0x6000 + i)), y, 1e-6})
		}
	}
	return out
}

func fzType(s *Session, syls string) proto.Snapshot {
	var snap proto.Snapshot
	for _, y := range strings.Fields(syls) {
		snap = typeSyl(s, y)
	}
	return snap
}

// listCandidates 在 from 開啟長度為 length 的候選並收集所有頁；視窗停在最後一頁。
func listCandidates(s *Session, from, length int) []proto.CandidateItem {
	s.Handle(proto.Request{Type: proto.Cursor, To: from})
	snap := s.Handle(proto.Request{Type: proto.Candidates, Length: length})
	if snap.Candidates == nil {
		return nil
	}
	items := slices.Clone(snap.Candidates.Items)
	for p := 1; p < snap.Candidates.Pages; p++ {
		items = append(items, s.Handle(proto.Request{Type: proto.Page, Index: p}).Candidates.Items...)
	}
	if s.cand.split > 0 {
		items = items[:s.cand.split] // 只比對這個詞長的候選；混合清單接在後面的單字另有測試（#161）
	}
	return items
}

func find(items []proto.CandidateItem, text string) (proto.CandidateItem, bool) {
	for _, it := range items {
		if it.Text == text {
			return it, true
		}
	}
	return proto.CandidateItem{}, false
}

func texts(items []proto.CandidateItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Text)
	}
	return out
}

func exactOnly(items []proto.CandidateItem) []string {
	var out []string
	for _, it := range items {
		if it.Note == "" {
			out = append(out, it.Text)
		}
	}
	return out
}

// F01–F12、F14–F19：每個目標詞以正常讀音列在詞庫，輸入是混淆後的音；近音候選可選，
// 附註是詞庫讀音；全部關閉時不可達。
func TestFuzzyTargetsAreReachable(t *testing.T) {
	for _, tc := range []struct {
		id, typed, word, matched string
	}{
		{"F01", "ㄗ ㄕˋ", "知識", "ㄓ ㄕˋ"},
		{"F02", "ㄗ ㄏㄡˋ", "之後", "ㄓ ㄏㄡˋ"},
		{"F03", "ㄓˇ ㄋㄩˇ", "子女", "ㄗˇ ㄋㄩˇ"},
		{"F04", "ㄓˋ ㄐㄧˇ", "自己", "ㄗˋ ㄐㄧˇ"},
		{"F05", "ㄘ ㄈㄢˋ", "吃飯", "ㄔ ㄈㄢˋ"},
		{"F06", "ㄘㄨ ㄇㄣˊ", "出門", "ㄔㄨ ㄇㄣˊ"},
		{"F07", "ㄔㄞˊ ㄌㄧㄠˋ", "材料", "ㄘㄞˊ ㄌㄧㄠˋ"},
		{"F08", "ㄔㄠˇ ㄇㄟˊ", "草莓", "ㄘㄠˇ ㄇㄟˊ"},
		{"F09", "ㄙㄥ ㄏㄨㄛˊ", "生活", "ㄕㄥ ㄏㄨㄛˊ"},
		{"F10", "ㄙㄣ ㄊㄧˇ", "身體", "ㄕㄣ ㄊㄧˇ"},
		{"F11", "ㄕㄢ ㄊㄧㄢ", "三天", "ㄙㄢ ㄊㄧㄢ"},
		{"F12", "ㄕㄨㄥˋ ㄒㄧㄣˋ", "送信", "ㄙㄨㄥˋ ㄒㄧㄣˋ"},
		{"F14", "ㄆㄣˊ ㄧㄡˇ", "朋友", "ㄆㄥˊ ㄧㄡˇ"},
		{"F15", "ㄧㄥ ㄨㄟˋ", "因為", "ㄧㄣ ㄨㄟˋ"},
		{"F16", "ㄅㄥˇ ㄌㄞˊ", "本來", "ㄅㄣˇ ㄌㄞˊ"},
		{"F17", "ㄒㄧㄣ ㄒㄧㄣ", "星星", "ㄒㄧㄥ ㄒㄧㄥ"},
		{"F18", "ㄘㄤˊ ㄘㄥˊ", "長城", "ㄔㄤˊ ㄔㄥˊ"},
		{"F19", "ㄙㄣ ㄧㄣ", "聲音", "ㄕㄥ ㄧㄣ"},
	} {
		n := len(strings.Fields(tc.typed))
		on := fuzzySession(allPairs(), fillers(tc.typed)...)
		fzType(on, tc.typed)
		it, ok := find(listCandidates(on, 0, n), tc.word)
		if !ok {
			t.Errorf("%s：%s → 候選沒有 %s", tc.id, tc.typed, tc.word)
			continue
		}
		if want := "近音 " + tc.matched; it.Note != want {
			t.Errorf("%s：%s 附註 %q，應為 %q", tc.id, tc.word, it.Note, want)
		}
		off := fuzzySession(fuzzy.Config{}, fillers(tc.typed)...)
		fzType(off, tc.typed)
		if _, ok := find(listCandidates(off, 0, n), tc.word); ok {
			t.Errorf("%s：全部關閉時 %s 不應可達", tc.id, tc.word)
		}
	}
}

// F21、F35：缺少設定（全部關閉）與只供選字都不改變自動顯示與提交；原音候選的相對次序不變。
func TestFuzzySelectModeKeepsDisplayAndExactOrder(t *testing.T) {
	for _, typed := range []string{"ㄧㄣ ㄨㄟˋ", "ㄧㄥ ㄍㄞ", "ㄗˇ ㄋㄩˇ", "ㄓ ㄕˋ", "ㄘ ㄈㄢˋ", "ㄨㄛˇ ㄅㄚˋ"} {
		off, on := fuzzySession(fuzzy.Config{}, fillers(typed)...), fuzzySession(allPairs(), fillers(typed)...)
		a, b := fzType(off, typed), fzType(on, typed)
		if a.Text() != b.Text() {
			t.Errorf("%s：顯示改變 %q → %q", typed, a.Text(), b.Text())
		}
		n := len(strings.Fields(typed))
		for l := 1; l <= n; l++ {
			want, got := texts(listCandidates(off, 0, l)), exactOnly(listCandidates(on, 0, l))
			// 單音節視窗最後一項是原樣注音，兩邊都有；合併後仍在最後。
			if !slices.Equal(want, got) {
				t.Errorf("%s 長度 %d：原音候選次序改變\n關閉 %v\n開啟 %v", typed, l, want, got)
			}
		}
		if got := on.Handle(proto.Request{Type: proto.Commit}).Commit; got != a.Text() {
			t.Errorf("%s：提交 %q，應為 %q", typed, got, a.Text())
		}
	}
}

// F26：只開一組，其他組不展開。
func TestFuzzyOnlyEnabledPairsExpand(t *testing.T) {
	s := fuzzySession(fuzzy.Config{}.With(fuzzy.ZZh, fuzzy.Select), fillers("ㄘ ㄈㄢˋ")...)
	fzType(s, "ㄘ ㄈㄢˋ")
	if _, ok := find(listCandidates(s, 0, 2), "吃飯"); ok {
		t.Error("只開 z-zh 時 c-ch 不應展開")
	}
	s = fuzzySession(fuzzy.Config{}.With(fuzzy.ZZh, fuzzy.Select), fillers("ㄗ ㄕˋ")...)
	fzType(s, "ㄗ ㄕˋ")
	if _, ok := find(listCandidates(s, 0, 2), "知識"); !ok {
		t.Error("z-zh 應展開 知識")
	}
}

// F27：同一詞面精確與近音都可達時歸原音，沒有附註，也只出現一次。
func TestFuzzySameSurfaceBelongsToExact(t *testing.T) {
	s := fuzzySession(allPairs(), word{"因", "ㄧㄥ", 8e-4}) // 因 另有 ㄧㄥ 讀音
	fzType(s, "ㄧㄣ")
	items := listCandidates(s, 0, 1)
	n := 0
	for _, it := range items {
		if it.Text == "因" {
			n++
			if it.Note != "" {
				t.Errorf("精確可達的詞面不應標近音：%+v", it)
			}
		}
	}
	if n != 1 {
		t.Errorf("因 出現 %d 次", n)
	}
	if it, ok := find(items, "應"); !ok || it.Note != "近音 ㄧㄥ" {
		t.Errorf("應 應為近音：%+v", it)
	}
}

// 第一頁保留固定位置給近音；原音候選保持次序；原樣注音仍在最後。
func TestFuzzyFirstPageEntryAndRawLast(t *testing.T) {
	var extra []word
	for i := range 25 {
		extra = append(extra, word{string(rune(0x4E00 + 100 + i)), "ㄗ", 1e-4 * float64(i+1)})
		extra = append(extra, word{string(rune(0x4E00 + 200 + i)), "ㄓ", 1e-4 * float64(i+1)})
	}
	s := fuzzySession(allPairs(), extra...)
	fzType(s, "ㄗ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	first := s.Handle(proto.Request{Type: proto.Candidates, Length: 1}).Candidates
	if first == nil || len(first.Items) != perPage {
		t.Fatalf("第一頁：%+v", first)
	}
	// M0 凍結：第一頁尾端 3 個位置保留給近音；其餘位置是原音候選。
	for i, it := range first.Items {
		if near := it.Note != ""; near != (i >= perPage-3) {
			t.Errorf("第 %d 項 %+v：近音只應占第一頁最後 3 個位置", i+1, it)
		}
	}
	all := listCandidates(s, 0, 1)
	if last := all[len(all)-1]; last.Text != "ㄗ" || last.Note != "" {
		t.Errorf("原樣注音應在最後：%+v", last)
	}
	// 精確候選的次序與關閉時相同。
	off := fuzzySession(fuzzy.Config{}, extra...)
	fzType(off, "ㄗ")
	if want, got := texts(listCandidates(off, 0, 1)), exactOnly(all); !slices.Equal(want, got) {
		t.Errorf("原音次序改變\n%v\n%v", want, got)
	}
}

// F28：開候選、翻頁、切換詞長、取消；取消不換首選、不丟組字。
func TestFuzzyCandidateNavigationAndCancel(t *testing.T) {
	s := fuzzySession(allPairs())
	before := fzType(s, "ㄗ ㄕˋ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	snap := s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	if snap.Candidates == nil {
		t.Fatal("沒有開候選")
	}
	// Tab 循環詞長：長度 2 只有近音（知識）也要能進入。
	two := false
	for range 3 {
		snap = key(s, proto.KeyTab)
		if snap.Candidates != nil && snap.Candidates.Length == 2 {
			two = true
			if _, ok := find(snap.Candidates.Items, "知識"); !ok {
				t.Errorf("詞長 2 第一頁應有近音 知識：%+v", snap.Candidates.Items)
			}
			break
		}
	}
	if !two {
		t.Error("Tab 沒有循環到只有近音的詞長")
	}
	snap = key(s, proto.KeyEscape)
	if snap.Candidates != nil || snap.Text() != before.Text() {
		t.Fatalf("取消後 %q（候選 %v），應為 %q", snap.Text(), snap.Candidates, before.Text())
	}
	if got := key(s, proto.KeyEnter).Commit; got != before.Text() {
		t.Fatalf("取消後提交 %q，應為 %q", got, before.Text())
	}
}

func fzSelect(t *testing.T, s *Session, from, length int, text string) {
	t.Helper()
	it, ok := find(listCandidates(s, from, length), text)
	if !ok {
		t.Fatalf("候選沒有 %q", text)
	}
	s.Handle(proto.Request{Type: proto.Select, Index: it.ID})
	s.Handle(proto.Request{Type: proto.Cursor, To: len(s.syls)}) // 回到句尾繼續輸入
}

// F29、F30：近音手選即鎖定；繼續輸入、移動游標、在前面插字都不跳回原音；刪掉該字才解除。
func TestFuzzyPickLocksAndFollowsEdits(t *testing.T) {
	s := fuzzySession(allPairs())
	fzType(s, "ㄗ")
	fzSelect(t, s, 0, 1, "知")
	if l := s.locks[0]; l.Word != "知" || l.Matched != "ㄓ" {
		t.Fatalf("鎖定應記錄詞庫讀音：%+v", l)
	}
	if now := s.Handle(proto.Request{Type: proto.Cursor, To: 1}); now.Text() != "知" || now.Preedit[0].Style != proto.StyleLocked {
		t.Fatalf("手選的近音字應顯示並標鎖定：%+v", now.Preedit)
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	// 鎖定的近音讀音仍在格中：繼續輸入後，含鎖定字的更長詞（知識）可以成詞，鎖定字不跳回原音。
	snap := typeSyl(s, "ㄕˋ")
	if snap.Text() != "知識" {
		t.Fatalf("繼續輸入後鎖定的近音字不得跳回：%q", snap.Text())
	}
	// 在前面插入：鎖定與讀音一起右移。
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	snap = typeSyl(s, "ㄅㄚˋ")
	if snap.Text() != "爸知識" {
		t.Fatalf("插入後 %q", snap.Text())
	}
	if l, ok := s.locks[1]; !ok || l.Matched != "ㄓ" {
		t.Fatalf("鎖定應平移並保留讀音：%+v", s.locks)
	}
	// 刪掉被鎖定的字才解除；之後恢復原音解碼。
	s.Handle(proto.Request{Type: proto.Cursor, To: 2})
	snap = key(s, proto.KeyBackspace)
	if len(s.locks) != 0 {
		t.Errorf("刪掉該字應解除鎖定：%+v", s.locks)
	}
	if snap.Text() != "爸是" {
		t.Errorf("刪除後 %q", snap.Text())
	}
}

// 鎖定存在，近音路徑就存在：之後關閉設定，已選的字不會消失；新輸入恢復原音通道。
func TestFuzzyLockSurvivesDisablingAndNewInputIsExact(t *testing.T) {
	s := fuzzySession(allPairs())
	fzType(s, "ㄗ")
	fzSelect(t, s, 0, 1, "知")
	s.Fuzzy = fuzzy.Config{}
	snap := typeSyl(s, "ㄕˋ")
	if snap.Text() != "知識" {
		t.Fatalf("關閉設定後已選的字不得消失：%q", snap.Text())
	}
	typeSyl(s, "ㄗ")
	if _, ok := find(listCandidates(s, 2, 1), "知"); ok {
		t.Error("關閉設定後新輸入不應再有近音候選")
	}
}

// 手選原音推翻先前的近音選擇：鎖定改為原音，不再保留近音讀音。
func TestFuzzyPickCanBeOverriddenByExact(t *testing.T) {
	s := fuzzySession(allPairs())
	fzType(s, "ㄗ")
	fzSelect(t, s, 0, 1, "知")
	fzSelect(t, s, 0, 1, "資")
	if l := s.locks[0]; l.Word != "資" || l.Matched != "" {
		t.Fatalf("改選原音後鎖定：%+v", l)
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "資" {
		t.Fatalf("提交 %q", got)
	}
}

// F32：跨音手選提交後不學成永久近音：沒有個人讀音、單字手選、片語、詞頻；一般精確手選照常學習。
func TestFuzzyPickIsNotLearned(t *testing.T) {
	s := fuzzySession(allPairs())
	fzType(s, "ㄨㄛˇ ㄗ ㄕˋ")
	fzSelect(t, s, 1, 2, "知識")
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "我知識" {
		t.Fatalf("提交 %q", got)
	}
	st := s.model.Store
	for k := range st.Readings {
		if strings.Contains(k, "知識") {
			t.Errorf("跨音手選不得記個人讀音：%q", k)
		}
	}
	if len(st.Picked) != 0 && (len(st.Picked["ㄗ"]) > 0 || len(st.Picked["ㄓ"]) > 0) {
		t.Errorf("不得記單字手選：%v", st.Picked)
	}
	for ph := range st.Phrases {
		if strings.Contains(ph, "知") {
			t.Errorf("不得晉升片語：%q", ph)
		}
	}
	if st.Uni["知識"] != 0 || st.Uses["知識"] != 0 {
		t.Errorf("不得計詞頻：%v %v", st.Uni["知識"], st.Uses["知識"])
	}
	// 新的 session 即使開啟設定，自動顯示也沒有偷偷學出的近音。
	s2 := New(s.model)
	fzType(s2, "ㄗ ㄕˋ")
	if snap := s2.Handle(proto.Request{Type: proto.Commit}); strings.Contains(snap.Commit, "知") {
		t.Errorf("新組字自動顯示出現近音：%q", snap.Commit)
	}
	// 控制組：精確手選仍然學習。
	s3 := fuzzySession(allPairs())
	fzType(s3, "ㄧㄣ")
	fzSelect(t, s3, 0, 1, "因")
	s3.Handle(proto.Request{Type: proto.Commit})
	if len(s3.model.Store.Readings) == 0 {
		t.Error("精確手選應照常記個人讀音")
	}
}

// F20：多個詞各自有自己的換音額度；單一詞超過上限的近音不展開，原音與逐字路徑仍可用。
func TestFuzzyBudgetIsPerWord(t *testing.T) {
	typed := "ㄕㄢ ㄘ ㄙㄜˊ ㄗ ㄕˋ" // 三池蛇 要 3 次換音；後面的 知識 要 1 次
	s := fuzzySession(allPairs(), append(fillers(typed), word{"三", "ㄙㄢ", 1e-3}, word{"池", "ㄔ", 1e-3}, word{"蛇", "ㄕㄜˊ", 1e-3}, word{"三池蛇", "ㄙㄢ ㄔ ㄕㄜˊ", 1e-3})...)
	fzType(s, typed)
	if _, ok := find(listCandidates(s, 0, 3), "三池蛇"); ok {
		t.Error("單詞 3 次換音超過上限，不應展開")
	}
	// 逐字各自可達：每個詞自己的額度，不是整句一次。
	for _, want := range []struct {
		from, length int
		text         string
	}{{0, 1, "三"}, {1, 1, "池"}, {2, 1, "蛇"}, {3, 2, "知識"}} {
		if _, ok := find(listCandidates(s, want.from, want.length), want.text); !ok {
			t.Errorf("位置 %d 長度 %d 應可選 %s", want.from, want.length, want.text)
		}
	}
	fzSelect(t, s, 3, 2, "知識")
	fzSelect(t, s, 1, 1, "池")
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; !strings.HasSuffix(got, "知識") || !strings.Contains(got, "池") {
		t.Errorf("兩處近音手選都應保留：%q", got)
	}
}

// 兩次換音以內的詞可達；超過者（3 次）不可達。
func TestFuzzySwapLimitIsTwo(t *testing.T) {
	extra := []word{{"之是師", "ㄓ ㄕˋ ㄕ", 1e-3}, {"之是史", "ㄓ ㄕˋ ㄕㄜˊ", 1e-3}}
	two := "ㄗ ㄙˋ ㄕ"   // ㄗ→ㄓ、ㄙˋ→ㄕˋ：2 次
	three := "ㄗ ㄙˋ ㄙ" // 再加 ㄙ→ㄕ：3 次
	for _, tc := range []struct {
		typed string
		want  bool
	}{{two, true}, {three, false}} {
		s := fuzzySession(allPairs(), append(fillers(tc.typed), extra...)...)
		fzType(s, tc.typed)
		if _, ok := find(listCandidates(s, 0, 3), "之是師"); ok != tc.want {
			t.Errorf("%s：之是師 可達=%v，應為 %v", tc.typed, ok, tc.want)
		}
	}
}

// F12 的真實情形：ㄕㄨㄥˋ 不在音節表，詞庫沒有任何字。關閉時這個音節照舊不收成音節（#109 起原樣留下）；
// 開啟 s-sh 後輸入端收進它、顯示維持原樣注音，開候選才出現 送，選取後鎖定。
func TestFuzzyAcceptsSyllableOnlyNearReadingsCanRead(t *testing.T) {
	off := fuzzySession(fuzzy.Config{})
	if snap := typeSyl(off, "ㄕㄨㄥˋ"); len(off.literals) != len(off.syls) || snap.Text() != "ㄕㄨㄥˋ" {
		t.Fatalf("關閉時不收成音節，連同聲調原樣留在畫面：%+v %q", off.syls, snap.Text())
	}
	s := fuzzySession(fuzzy.Config{}.With(fuzzy.SSh, fuzzy.Select))
	snap := fzType(s, "ㄕㄨㄥˋ ㄒㄧㄣˋ")
	if len(s.syls) != 2 {
		t.Fatalf("只能靠近音成詞的音節應被收進：%v", s.syls)
	}
	if strings.Contains(snap.Text(), "送") {
		t.Fatalf("自動顯示不得換成近音：%q", snap.Text())
	}
	fzSelect(t, s, 0, 2, "送信")
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "送信" {
		t.Fatalf("提交 %q", got)
	}
}

// 字面符號（標點）界定視窗：近音候選只在同一段內，不跨標點。
func TestFuzzyDoesNotCrossLiterals(t *testing.T) {
	s := fuzzySession(allPairs())
	fzType(s, "ㄗ")
	fzLiteral(s, "1") // 「,」在注音鍵盤是 ㄝ，不是字面符號；真正的字面符號要切英文打
	fzType(s, "ㄕˋ")
	if len(s.literals) != 1 {
		t.Fatalf("前提：要有一個字面符號：%v", s.literals)
	}
	for _, from := range []int{0, 2} {
		items := listCandidates(s, from, 1)
		if _, ok := find(items, "知識"); ok {
			t.Errorf("位置 %d 的候選不得含跨標點的詞", from)
		}
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	if snap := s.Handle(proto.Request{Type: proto.Candidates, Length: 2}); snap.Candidates != nil && snap.Candidates.Length == 2 {
		t.Error("視窗不得跨越字面符號")
	}
}

// 診斷輸出：附註格式固定，原音無附註。
func TestFuzzyNoteFormat(t *testing.T) {
	if nearNote("") != "" || nearNote("ㄓ ㄕˋ") != "近音 ㄓ ㄕˋ" {
		t.Fatal(nearNote("ㄓ ㄕˋ"))
	}
	items := listCandidates(func() *Session {
		s := fuzzySession(allPairs())
		fzType(s, "ㄗ")
		return s
	}(), 0, 1)
	for _, it := range items {
		if it.Note != "" && !strings.HasPrefix(it.Note, "近音 ") {
			t.Errorf("附註 %q", it.Note)
		}
		if it.Note == "" && fmt.Sprint(it.ID) == "" {
			t.Error("unreachable")
		}
	}
}

// F33：神經選字啟用時，原自動路徑照舊、評分器只看到原音候選（近音從不送進模型）；
// 近音手選之後鎖定，不再有模型回呼改寫已選的字。
func TestFuzzyNeverReachesTheNeuralScorer(t *testing.T) {
	var requests []rerank.Request
	typed := "ㄗ ㄕˋ ㄅㄚˋ"
	s := fuzzySession(allPairs(), fillers(typed)...)
	s.Opt.PrefixAware = false
	s.Rerank = rerank.Options{Weight: .1, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		requests = append(requests, r)
		return make([]float64, len(r.Candidates)), nil
	})}
	fzType(s, "ㄗ ㄕˋ")
	if len(requests) == 0 {
		t.Fatal("原自動路徑仍應送評分器")
	}
	for _, r := range requests {
		if !slices.Equal(r.Reading, zhuyin.ParseReading("ㄗ ㄕˋ")[:len(r.Reading)]) {
			t.Errorf("評分讀音必須是使用者打的音：%v", r.Reading)
		}
		for _, c := range r.Candidates {
			for _, near := range []string{"知識", "之後", "知"} {
				if strings.Contains(c.Text, near) {
					t.Errorf("近音候選 %q 不得送進評分器", c.Text)
				}
			}
			for _, g := range c.Segments {
				if g.Correction {
					t.Errorf("不得以 Correction 標記偷渡近音：%+v", g)
				}
			}
		}
	}
	fzSelect(t, s, 0, 2, "知識")
	calls := len(requests)
	snap := typeSyl(s, "ㄅㄚˋ")
	if snap.Text() != "知識爸" || len(requests) != calls {
		t.Fatalf("鎖定後不得有模型回呼改寫：%q，評分器呼叫 %d → %d", snap.Text(), calls, len(requests))
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "知識爸" {
		t.Fatalf("提交 %q", got)
	}
}

// 引擎把設定交給每個新 session。
func TestFuzzyDisabledByDefault(t *testing.T) {
	s := newSession()
	if s.Fuzzy.Enabled() {
		t.Fatal("缺少設定時不得啟用任何模糊音")
	}
	typeSyl(s, "ㄔ")
	for _, it := range listCandidates(s, 0, 1) {
		if it.Note != "" {
			t.Errorf("預設不應有近音候選：%+v", it)
		}
	}
}

// 重新開啟已鎖定近音的位置：由鎖定讀音到達的詞仍是近音候選（有附註），
// 原音候選與沒有鎖定時完全相同。
func TestFuzzyReopenKeepsExactCandidatesExact(t *testing.T) {
	typed := "ㄗ ㄕˋ"
	fresh := fuzzySession(allPairs(), fillers(typed)...)
	fzType(fresh, typed)
	want := exactOnly(listCandidates(fresh, 0, 2))

	s := fuzzySession(allPairs(), fillers(typed)...)
	fzType(s, typed)
	fzSelect(t, s, 0, 1, "知")
	items := listCandidates(s, 0, 2)
	if got := exactOnly(items); !slices.Equal(got, want) {
		t.Errorf("鎖定後原音候選改變\n%v\n%v", want, got)
	}
	if it, ok := find(items, "知識"); !ok || it.Note != "近音 ㄓ ㄕˋ" {
		t.Errorf("知識 應仍標為近音：%+v %v", it, ok)
	}
}

// 近音名額只計近音詞面：原音同音字再多（超過評分上限），近音候選也不被擠掉。
func TestFuzzyNearCandidatesAreNotCrowdedOutByExactOnes(t *testing.T) {
	var extra []word
	for i := range 90 {
		extra = append(extra, word{string(rune(0x4E00 + 300 + i)), "ㄕ", 1e-2}) // 原音同音字，先驗都比近音高
	}
	for i := range 5 {
		extra = append(extra, word{string(rune(0x4E00 + 500 + i)), "ㄙ", 1e-5})
	}
	s := fuzzySession(allPairs(), extra...)
	fzType(s, "ㄕ")
	items := listCandidates(s, 0, 1)
	near := 0
	for _, it := range items {
		if it.Note != "" {
			near++
		}
	}
	if near != 5 {
		t.Fatalf("5 個近音詞面都應列出，實際 %d（共 %d 項）", near, len(items))
	}
	first := s.Handle(proto.Request{Type: proto.Page, Index: 0}).Candidates.Items
	if n := len(first); n != perPage || first[perPage-1].Note == "" {
		t.Errorf("第一頁尾端應有近音：%+v", first)
	}
}

// 近音才能成詞的音節被接受之後，守門仍然有效：之後打出沒有任何路徑的音節照舊不收成音節
// （#109 起原樣留下），組字區不會因為目前顯示的是原樣注音就什麼都收。
func TestFuzzyKeepsTheJunkSyllableGuardAfterANearOnlySyllable(t *testing.T) {
	// 信：原樣留下的 ㄅㄨㄛˇ 是解碼邊界，其後的 ㄒㄧㄣˋ 要自己成字（真實詞庫每個合法音節都有字）。
	s := fuzzySession(fuzzy.Config{}.With(fuzzy.SSh, fuzzy.Select), word{"送", "ㄙㄨㄥˋ", 1e-3}, word{"信", "ㄒㄧㄣˋ", 1e-3})
	typeSyl(s, "ㄕㄨㄥˋ") // 精確沒有字，近音 ㄙㄨㄥˋ 可成詞
	if len(s.syls) != 1 {
		t.Fatalf("近音可成詞的音節應被接受：%v", s.syls)
	}
	typeSyl(s, "ㄅㄨㄛˇ") // ㄅㄨㄛˇ 沒有字，近音也沒有：多半是按錯鍵
	if len(s.syls) != 5 || s.literals[0] || len(s.literals) != 4 || zhuyin.Key(s.syls[1:]) != "ㄅ ㄨ ㄛ ˇ" {
		t.Fatalf("按錯鍵的音節不收成音節，原樣留下，現有音節 %v", s.syls)
	}
	typeSyl(s, "ㄒㄧㄣˋ") // 正常音節照常收進來
	if len(s.syls) != 6 || s.literals[5] {
		t.Fatalf("正常音節應被接受：%v", s.syls)
	}
}

// 審查 #1：近音鎖定的詞被標點切開時，兩半各帶走自己的詞庫讀音，路徑不消失，之後繼續輸入也不變回注音。
func TestFuzzyLockSurvivesALiteralInsertedInTheMiddle(t *testing.T) {
	typed := "ㄗ ㄉㄠˋ"
	s := fuzzySession(allPairs(), append(fillers(typed), word{"知道", "ㄓ ㄉㄠˋ", 1e-3}, word{"道", "ㄉㄠˋ", 1e-3}, word{"嗎", "ㄇㄚ˙", 1e-3})...)
	fzType(s, typed)
	fzSelect(t, s, 0, 2, "知道")
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	snap := key(s, "A")
	if snap.Text() != "知A道" {
		t.Fatalf("標點插入後 %q", snap.Text())
	}
	left, right := s.locks[0], s.locks[2]
	if left.Matched != "ㄓ" || right.Matched != "ㄉㄠˋ" {
		t.Fatalf("兩半應各帶走自己的讀音：%+v／%+v", left, right)
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: len(s.syls)})
	if got := typeSyl(s, "ㄇㄚ˙").Text(); got != "知A道嗎" {
		t.Fatalf("之後繼續輸入不得整段退成注音：%q", got)
	}
}

// 審查 #2：打出一個只能靠近音成詞的音節，前面已顯示的字不變回注音，提交也不丟字。
func TestFuzzyNearOnlySyllableKeepsEarlierText(t *testing.T) {
	s := fuzzySession(fuzzy.Config{}.With(fuzzy.SSh, fuzzy.Select), word{"送", "ㄙㄨㄥˋ", 1e-3})
	before := fzType(s, "ㄨㄛˇ")
	if before.Text() != "我" {
		t.Fatalf("前提：%q", before.Text())
	}
	snap := typeSyl(s, "ㄕㄨㄥˋ")
	if snap.Text() != "我ㄕㄨㄥˋ" {
		t.Fatalf("前面已顯示的字應保留，只有近音音節顯示原樣注音：%q", snap.Text())
	}
	fzSelect(t, s, 1, 1, "送")
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "我送" {
		t.Fatalf("選近音候選後提交 %q", got)
	}
	if s.nearReading {
		t.Error("路徑恢復後保底旗標應清除")
	}
}

// 使用者回報：「揣摩上意」第一個字打錯成 ㄗㄨㄞˇ（精確沒有字，近音 ㄓㄨㄞˇ 才有），後面整段都只顯示注音、
// 提交也是注音。只有那個音節留注音，其後的音節照常出字；改打正確讀音後整段恢復正常解碼。
func TestFuzzyNearOnlySyllableDoesNotBlankLaterText(t *testing.T) {
	s := fuzzySession(fuzzy.Config{}.With(fuzzy.SSh, fuzzy.Select), word{"送", "ㄙㄨㄥˋ", 1e-3}, word{"信", "ㄒㄧㄣˋ", 1e-3}, word{"給", "ㄍㄟˇ", 1e-3}, word{"他", "ㄊㄚ", 1e-3})
	snap := fzType(s, "ㄕㄨㄥˋ ㄍㄟˇ ㄊㄚ")
	if !s.nearReading || len(s.path) != 0 {
		t.Fatalf("前提：只因近音收進，精確解不出路徑：near=%v path=%v", s.nearReading, s.path)
	}
	if snap.Text() != "ㄕㄨㄥˋ給他" {
		t.Fatalf("近音音節後面的音節要照常出字：%q", snap.Text())
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "ㄕㄨㄥˋ給他" {
		t.Fatalf("提交 %q", got)
	}
	if len(s.model.Store.Corrections) != 0 {
		t.Fatalf("保底文字不是解碼路徑，不學習：%+v", s.model.Store.Corrections)
	}

	s = fuzzySession(fuzzy.Config{}.With(fuzzy.SSh, fuzzy.Select), word{"送", "ㄙㄨㄥˋ", 1e-3}, word{"給", "ㄍㄟˇ", 1e-3}, word{"他", "ㄊㄚ", 1e-3})
	fzType(s, "ㄕㄨㄥˋ ㄍㄟˇ ㄊㄚ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	key(s, proto.KeyBackspace)
	if snap := typeSyl(s, "ㄙㄨㄥˋ"); snap.Text() != "送給他" || s.nearReading {
		t.Fatalf("改打正確讀音後整段照常解碼：%q near=%v", snap.Text(), s.nearReading)
	}
}

// 保底狀態下照常出的字也照常送神經選字；前文只到沒有字的音節為止（左邊是原樣注音，不接文件前文）。
func TestFuzzyNearOnlySyllableTextIsReranked(t *testing.T) {
	s := fuzzySession(fuzzy.Config{}.With(fuzzy.SSh, fuzzy.Select), word{"送", "ㄙㄨㄥˋ", 1e-3}, word{"給", "ㄍㄟˇ", 1e-3}, word{"他", "ㄊㄚ", 1e-3}, word{"她", "ㄊㄚ", 1e-4})
	s.Opt.PrefixAware = false
	s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
	var contexts []string
	s.Rerank = rerank.Options{Weight: .1, ContextRunes: rerank.MaxContextRunes, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		contexts = append(contexts, r.Context)
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			if strings.Contains(c.Text, "她") {
				scores[i] = 1000
			}
		}
		return scores, nil
	})}
	fzType(s, "ㄨㄛˇ")
	c := key(s, proto.KeyEnter)
	s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: c.ContextID})
	contexts = nil
	snap := fzType(s, "ㄕㄨㄥˋ ㄍㄟˇ ㄊㄚ")
	if snap.Text() != "ㄕㄨㄥˋ給她" || len(s.path) != 0 || snap.Suggestion != "" {
		t.Fatalf("保底文字應採神經的選擇、不留路徑與提示：%q path=%v suggestion=%q", snap.Text(), s.path, snap.Suggestion)
	}
	if len(contexts) == 0 || contexts[len(contexts)-1] != "" {
		t.Fatalf("原樣注音之後的段落不得帶文件前文：%q", contexts)
	}
	if got := key(s, proto.KeyEnter).Commit; got != "ㄕㄨㄥˋ給她" {
		t.Fatalf("提交 %q", got)
	}
}

// 審查 #3：與視窗部分重疊的近音鎖定，不能讓它留在視窗外的讀音影響原音候選的前後文。
func TestFuzzyOverlappingLockDoesNotChangeExactCandidateOrder(t *testing.T) {
	extra := []word{{"知世", "ㄓ ㄕˋ", 1e-3}, {"世", "ㄕˋ", 1e-4}}
	typed := "ㄗ ㄕˋ"
	build := func() *Session {
		lex, sys := fuzzyLexicon(append(fillers(typed), extra...)...)
		sys.SetBi("知世", "是", 1e-9) // 無關的雙連詞，確保有上下文狀態
		sys.SetBi("知", "世", 0.9)   // 左邊是「知」時，世 遠勝 是
		s := New(personal.NewModel(lex, sys))
		s.Fuzzy = allPairs()
		fzType(s, typed)
		return s
	}
	fresh := build()
	want := exactOnly(listCandidates(fresh, 1, 1))
	s := build()
	fzSelect(t, s, 0, 2, "知世")
	if got := exactOnly(listCandidates(s, 1, 1)); !slices.Equal(got, want) {
		t.Fatalf("鎖定重疊時原音候選次序不得受鎖定讀音影響\n%v\n%v", want, got)
	}
}
