package engine

import (
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/layout"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/vocab"
	"git.biggo.com/0c/ime/core/zhuyin"
)

const armstrong = "新阿姆斯特朗回旋加速噴氣式阿姆斯特朗砲"
const armstrongReading = "ㄒㄧㄣ ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ ㄏㄨㄟˊ ㄒㄩㄢˊ ㄐㄧㄚ ㄙㄨˋ ㄆㄣ ㄑㄧˋ ㄕˋ ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ ㄆㄠˋ"

// vocabEngine 的詞庫有每個字的單字，加上「世界」和幾個一般詞；單字拼湊的機率比自訂詞的下限低。
func vocabEngine() *Engine {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	add := func(w, r string, p float64) {
		lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading(r), PRW: 1})
		sys.SetUni(w, p)
	}
	syls := zhuyin.ParseReading(armstrongReading)
	for i, r := range []rune(armstrong) {
		add(string(r), string(syls[i]), 1e-4)
	}
	sys.SetUni("新", 1e-2) // 常用字
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"是", "ㄕˋ", 1e-2}, {"界", "ㄐㄧㄝˋ", 1e-4}, {"介", "ㄐㄧㄝˋ", 1e-4}, {"結", "ㄐㄧㄝˊ", 2e-5}, {"傑", "ㄐㄧㄝˊ", 4e-5}, {"世", "ㄕˋ", 4e-5},
		{"世界", "ㄕˋ ㄐㄧㄝˋ", 5e-4}, {"阿姆斯特朗", "ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ", 1e-6}, {"加速", "ㄐㄧㄚ ㄙㄨˋ", 1e-5},
		{"阿姆斯特丹", "ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄉㄢ", 1e-3}, {"丹", "ㄉㄢ", 1e-4},
		// 各字數的分布：自訂詞下限取第 90 百分位
		{"甲乙", "ㄐㄧㄚˇ ㄧˇ", 1e-6}, {"丙丁", "ㄅㄧㄥˇ ㄉㄧㄥ", 2e-6},
		// 真實資料包最長的系統詞是 9 字、機率在最低值：比它長的自訂詞下限很低，補全要靠親手加入的證據
		{"甲乙丙丁戊己庚辛壬", "ㄐㄧㄚˇ ㄧˇ ㄅㄧㄥˇ ㄉㄧㄥ ㄨˋ ㄐㄧˇ ㄍㄥ ㄒㄧㄣ ㄖㄣˊ", 5e-8},
	} {
		add(e.w, e.r, e.p)
	}
	return New(lex, sys)
}

func typeReading(e *Engine, id uint64, reading string) proto.Snapshot {
	var snap proto.Snapshot
	for _, syl := range zhuyin.ParseReading(reading) {
		for _, k := range layout.Standard.Keys(syl) {
			snap, _ = e.Do(id, proto.Request{Type: proto.Key, Key: string(k)})
		}
	}
	return snap
}

func TestVocabLongWordAndHomophones(t *testing.T) {
	e := vocabEngine()
	if got := typeReading(e, e.OpenSession(), armstrongReading).Text(); got == armstrong {
		t.Fatal("加入前不該剛好打得出來，測試沒有意義")
	}
	r := e.Vocab(VocabRequest{Op: VocabAdd, Words: []vocab.Entry{{Text: armstrong, Readings: []string{armstrongReading}}, {Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}, {Text: "世介", Readings: []string{"ㄕˋ ㄐㄧㄝˋ"}}}})
	if !r.OK || !r.Changed || r.Count != 3 || r.Batch == nil {
		t.Fatalf("%+v", r)
	}
	// 19 音節超過系統詞的解碼上限 8，仍然一次打得出來。
	if got := typeReading(e, e.OpenSession(), armstrongReading).Text(); got != armstrong {
		t.Fatalf("長自訂詞：%q", got)
	}
	// 沒有強勢同音詞：自訂詞直接是首選。
	if got := typeReading(e, e.OpenSession(), "ㄕˋ ㄐㄧㄝˊ").Text(); got != "世傑" {
		t.Fatalf("世傑：%q", got)
	}
	// 有常用同音詞：首選讓給「世界」，但自訂詞在候選第一頁。
	id := e.OpenSession()
	if got := typeReading(e, id, "ㄕˋ ㄐㄧㄝˋ").Text(); got != "世界" {
		t.Fatalf("常用同音詞照樣首選：%q", got)
	}
	snap, _ := e.Do(id, proto.Request{Type: proto.Key, Key: proto.KeyDown})
	if snap.Candidates == nil || len(snap.Candidates.Items) < 2 || snap.Candidates.Items[1].Text != "世介" {
		t.Fatalf("自訂詞保證在第一頁：%+v", snap.Candidates)
	}
	ranks := e.Vocab(VocabRequest{Op: VocabRank}).Ranks
	byWord := map[string]Rank{}
	for _, r := range ranks {
		byWord[r.Text] = r
	}
	if !byWord[armstrong].First || !byWord["世傑"].First || byWord["世介"].First || byWord["世介"].Ahead != "世界" || byWord["世介"].Page != 1 {
		t.Fatalf("排名：%+v", ranks)
	}
}

// 自訂詞在同長度的候選裡排第一、組字卻解成別的拆法（加詞後又照預設提交了很多次「是傑」）：
// 排名要說贏過它的是解碼結果，不是空白。
func TestVocabRankAheadIsDecodedSplit(t *testing.T) {
	e := vocabEngine()
	e.Vocab(VocabRequest{Op: VocabAdd, Words: []vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}})
	s := e.Model.Store
	for k, v := range map[string]float64{lm.BOS + "\t是": 8, "是\t傑": 8} {
		s.Bi[k] = v
	}
	s.Ctx[lm.BOS], s.Ctx["是"], s.Uni["是"], s.Uni["傑"], s.Total = 8, 8, 8, 8, 16
	e.Model.Gen++
	if got := typeReading(e, e.OpenSession(), "ㄕˋ ㄐㄧㄝˊ").Text(); got != "是傑" {
		t.Fatalf("前提：學過的拆法勝出：%q", got)
	}
	r := e.Vocab(VocabRequest{Op: VocabRank, Texts: []string{"世傑"}}).Ranks
	if len(r) != 1 || r[0].First || r[0].Index != 1 || r[0].Ahead != "是傑" {
		t.Fatalf("%+v", r)
	}
}

func TestVocabRemoveUndoAndStatus(t *testing.T) {
	e := vocabEngine()
	add := e.Vocab(VocabRequest{Op: VocabAdd, Source: vocab.SourceAssistant, Words: []vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}})
	rm := e.Vocab(VocabRequest{Op: VocabRemove, Texts: []string{"世傑"}})
	if !rm.OK || rm.Count != 0 {
		t.Fatalf("%+v", rm)
	}
	if got := typeReading(e, e.OpenSession(), "ㄕˋ ㄐㄧㄝˊ").Text(); got == "世傑" {
		t.Fatal("刪除後立即生效")
	}
	if u := e.Vocab(VocabRequest{Op: VocabUndo, Batch: rm.Batch.ID}); !u.OK || u.Count != 1 {
		t.Fatalf("%+v", u)
	}
	bs := e.Vocab(VocabRequest{Op: VocabBatches}).Batches
	if len(bs) != 3 || bs[0].Op != "undo" || bs[2].ID != add.Batch.ID || bs[2].Source != vocab.SourceAssistant || bs[2].Preview[0] != "世傑" {
		t.Fatalf("%+v", bs)
	}
	bad := e.Vocab(VocabRequest{Op: VocabAdd, Words: []vocab.Entry{{Text: "a c", Readings: []string{"ㄅ"}}}})
	if bad.OK || bad.Error == nil || bad.Error.Code != vocab.CodeInvalidWord || bad.Changed || bad.Count != 1 {
		t.Fatalf("%+v", bad)
	}
	if st := e.Vocab(VocabRequest{Op: VocabStatus}); !st.OK || st.Limit != vocab.MaxWords || st.Version != 3 {
		t.Fatalf("%+v", st)
	}
	if l := e.Vocab(VocabRequest{Op: VocabList, Query: "ㄕˋ"}); l.Total != 1 || l.Words[0].Text != "世傑" {
		t.Fatalf("依讀音前綴篩選：%+v", l)
	}
	if l := e.Vocab(VocabRequest{Op: VocabList, Query: "傑"}); l.Total != 1 {
		t.Fatalf("依詞面篩選：%+v", l)
	}
}

// 學到的片語可以轉成自訂詞，之後不受衰減與清理影響。
func TestVocabKeepLearnedPhrase(t *testing.T) {
	e := vocabEngine()
	e.Model.Store.Phrases["世結"] = "ㄕˋ ㄐㄧㄝˊ"
	e.Model.Store.PhraseOrder = append(e.Model.Store.PhraseOrder, "世結")
	e.Model.Store.PhraseSeen["世結"] = 3
	e.Model.Store.Readings["世界\tㄕˋ ㄐㄧㄝˋ"] = true
	e.Model.Store.Readings["世\tㄕˋ"] = true // 單字的手選不列
	l := e.Vocab(VocabRequest{Op: VocabLearned}).Learned
	if len(l) != 2 || l[0].Text != "世結" || l[0].Kind != "phrase" || l[0].Uses != 3 || l[0].Vocab || l[1].Text != "世界" || l[1].Kind != "reading" {
		t.Fatalf("%+v", l)
	}
	if r := e.Vocab(VocabRequest{Op: VocabKeep, Texts: []string{"不存在"}}); r.OK || r.Error.Code != vocab.CodeNotFound {
		t.Fatalf("%+v", r)
	}
	r := e.Vocab(VocabRequest{Op: VocabKeep, Texts: []string{"世結"}})
	if !r.OK || r.Count != 1 {
		t.Fatalf("%+v", r)
	}
	if w, _ := e.Model.Vocab.Lookup("世結"); w.Source != vocab.SourceLearned {
		t.Fatalf("%+v", w)
	}
	if l := e.Vocab(VocabRequest{Op: VocabLearned}).Learned; !l[0].Vocab {
		t.Fatal("學到的清單要標出已經是自訂詞")
	}
	if r := e.Vocab(VocabRequest{Op: VocabKeep, Texts: []string{"世界"}}); !r.OK {
		t.Fatalf("親手選過讀音的詞也能保留：%+v", r)
	}
	if w, _ := e.Model.Vocab.Lookup("世界"); w.Readings[0] != "ㄕˋ ㄐㄧㄝˋ" {
		t.Fatalf("%+v", w)
	}
}

// 親手加入先前忘記的詞：解除 tombstone，被動使用又可以學。
func TestVocabAddClearsTombstone(t *testing.T) {
	e := vocabEngine()
	e.Model.Forget("世傑")
	e.Vocab(VocabRequest{Op: VocabAdd, Words: []vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}})
	if e.Model.Store.Tombstones["世傑"] {
		t.Fatal("親手加入要解除 tombstone")
	}
}

func TestVocabPhrasesAndSnapshot(t *testing.T) {
	e := vocabEngine()
	r := e.Vocab(VocabRequest{Op: VocabPhraseAdd, Texts: []string{"——", "台北市"}})
	if !r.OK || len(r.Phrases) != 2 || !r.Changed {
		t.Fatalf("%+v", r)
	}
	snap := e.VocabSnapshot()
	e.Vocab(VocabRequest{Op: VocabPhraseRemove, IDs: []uint64{1}})
	if len(snap.Phrases()) != 2 || len(e.Vocab(VocabRequest{Op: VocabPhrases}).Phrases) != 1 {
		t.Fatal("快照與之後的修改無關")
	}
	if r := e.Vocab(VocabRequest{Op: "nope"}); r.OK || !strings.Contains(r.Error.Msg, "nope") {
		t.Fatalf("%+v", r)
	}
}

// 長自訂詞走 Tab 補全：打兩個音節起淡色顯示後半，按 Tab 收下；解碼把「新阿姆」切成「新｜阿姆…」時
// 尾端只看得到系統詞「阿姆斯特丹」，自訂詞要從「新」那一段找起。只打一個字不建議。
func TestVocabTabCompletion(t *testing.T) {
	e := vocabEngine()
	syls := zhuyin.ParseReading(armstrongReading)
	before := typeReading(e, e.OpenSession(), zhuyin.Key(syls[:3]))
	if strings.Contains(before.Suggestion, "回旋") {
		t.Fatalf("加入前不會補自訂詞：%q", before.Suggestion)
	}
	e.Vocab(VocabRequest{Op: VocabAdd, Words: []vocab.Entry{{Text: armstrong, Readings: []string{armstrongReading}}}})
	if got := typeReading(e, e.OpenSession(), zhuyin.Key(syls[:1])); got.Suggestion != "" {
		t.Fatalf("只打一個音節不建議：%q", got.Suggestion)
	}
	id := e.OpenSession()
	snap := typeReading(e, id, zhuyin.Key(syls[:3]))
	if snap.SuggestedText() != armstrong {
		t.Fatalf("補全：%q＋%q", snap.Text(), snap.Suggestion)
	}
	snap, _ = e.Do(id, proto.Request{Type: proto.Key, Key: proto.KeyTab})
	if snap.Text() != armstrong {
		t.Fatalf("Tab 收下：%q", snap.Text())
	}
	snap, _ = e.Do(id, proto.Request{Type: proto.Key, Key: proto.KeyEnter})
	if snap.Commit != armstrong {
		t.Fatalf("提交：%q", snap.Commit)
	}
}

// 讀音建議：整個詞在詞庫裡用它的讀音；單獨成段的破音字（曾）標出來讓使用者選，詞裡的不標；
// 查不到的字回報 Unknown。
func TestVocabAnnotate(t *testing.T) {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	add := func(w, r string, prw, p float64) {
		lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading(r), PRW: prw})
		sys.SetUni(w, p)
	}
	add("曾", "ㄘㄥˊ", 1, 1e-4)
	add("曾", "ㄗㄥ", 0.1, 1e-4)
	add("曾經", "ㄘㄥˊ ㄐㄧㄥ", 1, 3e-4)
	add("曾孫", "ㄗㄥ ㄙㄨㄣ", 1, 5e-5)
	add("小", "ㄒㄧㄠˇ", 1, 1e-3)
	add("明", "ㄇㄧㄥˊ", 1, 1e-3)
	add("明", "ㄇㄧㄥ˙", 0.1, 1e-3)
	add("明白", "ㄇㄧㄥ˙ ㄅㄞˊ", 1, 1e-4) // 輕聲變體不算破音
	add("小明", "ㄒㄧㄠˇ ㄇㄧㄥˊ", 1, 1e-5)
	e := New(lex, sys)
	r := e.Vocab(VocabRequest{Op: VocabAnnotate, Texts: []string{"曾小明", "曾經", "曾小🙂", "明"}})
	if !r.OK || len(r.Annotations) != 4 {
		t.Fatalf("%+v", r)
	}
	a := r.Annotations[0]
	if a.Reading != "ㄘㄥˊ ㄒㄧㄠˇ ㄇㄧㄥˊ" || !a.Chars[0].Ambiguous || a.Chars[1].Ambiguous || a.Chars[2].Ambiguous || strings.Join(a.Chars[0].Options, " ") != "ㄘㄥˊ ㄗㄥ" || a.Exists {
		t.Fatalf("曾小明：%+v", a)
	}
	if b := r.Annotations[1]; b.Reading != "ㄘㄥˊ ㄐㄧㄥ" || b.Chars[0].Ambiguous || !b.Exists {
		t.Fatalf("詞裡的破音字由詞決定：%+v", b)
	}
	if c := r.Annotations[2]; !c.Unknown || c.Reading != "" || c.Chars[0].Reading != "ㄘㄥˊ" {
		t.Fatalf("查不到的字：%+v", c)
	}
	if d := r.Annotations[3]; d.Chars[0].Ambiguous {
		t.Fatalf("輕聲變體不算破音：%+v", d)
	}
}

// 改讀音時帶上看到的原讀音：別人剛改過就拒絕，不悄悄覆蓋（管理頁與 AI 助手同時編輯）。
func TestVocabReadingsConflict(t *testing.T) {
	e := vocabEngine()
	e.Vocab(VocabRequest{Op: VocabAdd, Words: []vocab.Entry{{Text: "曾國城", Readings: []string{"ㄘㄥˊ ㄍㄨㄛˊ ㄔㄥˊ"}}}})
	seen := []string{"ㄘㄥˊ ㄍㄨㄛˊ ㄔㄥˊ"}
	if r := e.Vocab(VocabRequest{Op: VocabReadings, Source: vocab.SourceAssistant, Text: "曾國城", Readings: []string{"ㄗㄥ ㄍㄨㄛˊ ㄔㄥˊ"}}); !r.OK {
		t.Fatalf("%+v", r)
	}
	r := e.Vocab(VocabRequest{Op: VocabReadings, Text: "曾國城", Readings: []string{"ㄘㄥˊ ㄍㄨㄛˊ ㄔㄥˊ", "ㄗㄥ ㄍㄨㄛˊ ㄔㄥˊ"}, Expect: seen})
	if r.OK || r.Error.Code != vocab.CodeConflict {
		t.Fatalf("過期的修改要拒絕：%+v", r)
	}
	if w, _ := e.Model.Vocab.Lookup("曾國城"); len(w.Readings) != 1 || w.Readings[0] != "ㄗㄥ ㄍㄨㄛˊ ㄔㄥˊ" {
		t.Fatalf("%+v", w)
	}
}

// #146：英文詞經指令新增、列出、刪除；新增後混打的新工作階段打 ls 照英文送出。
// 排名與標注音只對漢字有意義，英文詞略過；刪除英文詞不動自動學到的英文字。
func TestVocabEnglishWords(t *testing.T) {
	e := vocabEngine()
	e.Model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: "孬", Reading: zhuyin.ParseReading("ㄋㄠ"), PRW: 1})
	e.Model.LM.(*lm.Mem).SetUni("孬", 1e-5)
	e.MixedInput, e.Preview02 = true, true
	typeKeys := func(keys string) string {
		id := e.OpenSession()
		for _, k := range keys {
			e.Do(id, proto.Request{Type: proto.Key, Key: string(k)})
		}
		snap, _ := e.Do(id, proto.Request{Type: proto.Key, Key: proto.KeyEnter})
		return snap.Commit
	}
	if got := typeKeys("ls "); got != "孬" {
		t.Fatalf("測試前提：沒加英文詞時 ls␣ 是孬，實得 %q", got)
	}
	add := e.Vocab(VocabRequest{Op: VocabAdd, Words: []vocab.Entry{{Text: "ls"}, {Text: "kubectl"}, {Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}})
	if !add.OK || add.Count != 3 || !add.Changed {
		t.Fatalf("%+v", add)
	}
	if got := typeKeys("ls "); got != "ls " {
		t.Fatalf("加入後 ls␣ 照英文送出，實得 %q", got)
	}
	if l := e.Vocab(VocabRequest{Op: VocabList, Query: "kube"}); l.Total != 1 || !l.Words[0].English() || l.Words[0].Readings == nil {
		t.Fatalf("%+v", l)
	}
	if r := e.Vocab(VocabRequest{Op: VocabRank}); len(r.Ranks) != 1 || r.Ranks[0].Text != "世傑" {
		t.Fatalf("排名略過英文詞：%+v", r.Ranks)
	}
	if a := e.Vocab(VocabRequest{Op: VocabAnnotate, Texts: []string{"ls", "世傑"}}); !a.Annotations[0].English || a.Annotations[0].Unknown || a.Annotations[1].English {
		t.Fatalf("%+v", a.Annotations)
	}
	e.Model.LearnEnglish([]personal.EnglishCommit{{Text: "kubectl", Explicit: true}})
	if _, ok := e.Model.Store.English["kubectl"]; !ok {
		t.Fatal("測試前提：學到 kubectl")
	}
	if r := e.Vocab(VocabRequest{Op: VocabRemove, Texts: []string{"ls", "kubectl"}}); !r.OK || r.Count != 1 {
		t.Fatalf("%+v", r)
	}
	if _, ok := e.Model.Store.English["kubectl"]; !ok || e.Model.Store.Tombstones["kubectl"] {
		t.Fatal("刪除英文詞不能忘記學到的英文字")
	}
	if got := typeKeys("ls "); got != "孬" {
		t.Fatalf("刪除後回到原本的行為，實得 %q", got)
	}
}
