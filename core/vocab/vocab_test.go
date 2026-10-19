package vocab

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/zhuyin"
)

const armstrong = "新阿姆斯特朗回旋加速噴氣式阿姆斯特朗砲"
const armstrongReading = "ㄒㄧㄣ ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ ㄏㄨㄟˊ ㄒㄩㄢˊ ㄐㄧㄚ ㄙㄨˋ ㄆㄣ ㄑㄧˋ ㄕˋ ㄚ ㄇㄨˇ ㄙ ㄊㄜˋ ㄌㄤˇ ㄆㄠˋ"

func code(t *testing.T, err error) string {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("要回傳 *Error：%v", err)
	}
	return e.Code
}

func TestAddLookupAndLongPrefix(t *testing.T) {
	s := New()
	b, err := s.Add([]Entry{{armstrong, []string{armstrongReading}}, {"陳怡君", []string{"ㄔㄣˊ  ㄧˊ ㄐㄩㄣ"}}}, SourceManual, 100)
	if err != nil || b == nil || b.ID != 1 || len(b.Changes) != 2 {
		t.Fatalf("%+v %v", b, err)
	}
	if s.Len() != 2 || s.Version() != 1 || s.Longest() != 19 {
		t.Fatalf("len %d version %d longest %d", s.Len(), s.Version(), s.Longest())
	}
	w, ok := s.Lookup("陳怡君")
	if !ok || w.Readings[0] != "ㄔㄣˊ ㄧˊ ㄐㄩㄣ" || w.Source != SourceManual || w.Added != 100 || w.Batch != 1 {
		t.Fatalf("讀音要整理空白：%+v", w)
	}
	syls := zhuyin.ParseReading(armstrongReading)
	if m := s.Exact(syls); len(m) != 1 || m[0].Text != armstrong {
		t.Fatalf("完整讀音：%+v", m)
	}
	if !s.HasPrefix(syls[:3]) || !s.HasPrefix(syls[:12]) || s.HasPrefix(zhuyin.ParseReading("ㄒㄧㄣ ㄇㄨˇ")) {
		t.Fatal("前綴")
	}
	if m := s.Prefixed(syls[:3], 0); len(m) != 1 || len(m[0].Reading) != 19 {
		t.Fatalf("前綴查詢要帶完整讀音：%+v", m)
	}
	if !s.HasReading(armstrong, armstrongReading) || s.HasReading(armstrong, "ㄒㄧㄣ") {
		t.Fatal("HasReading")
	}
}

func TestAddMergesReadingsAndSkipsNoop(t *testing.T) {
	s := New()
	if _, err := s.Add([]Entry{{"曾", []string{"ㄗㄥ"}}}, SourceManual, 1); err != nil {
		t.Fatal(err)
	}
	b, err := s.Add([]Entry{{"曾", []string{"ㄘㄥˊ", "ㄗㄥ"}}}, SourceCompose, 2)
	if err != nil || len(b.Changes) != 1 || b.Changes[0].Before == nil {
		t.Fatalf("已存在的詞合併讀音：%+v %v", b, err)
	}
	if w, _ := s.Lookup("曾"); strings.Join(w.Readings, "|") != "ㄗㄥ|ㄘㄥˊ" || w.Source != SourceManual || w.Added != 1 {
		t.Fatalf("保留原來源與時間：%+v", w)
	}
	if b, err := s.Add([]Entry{{"曾", []string{"ㄘㄥˊ"}}}, SourceManual, 3); b != nil || err != nil || s.Version() != 2 {
		t.Fatalf("沒有變更不記批次：%+v %v", b, err)
	}
}

func TestValidationRejectsWholeBatch(t *testing.T) {
	for _, tc := range []struct {
		e    Entry
		code string
	}{
		{Entry{"", []string{"ㄅ"}}, CodeInvalidWord},
		{Entry{"abc", []string{"ㄅ ㄅ ㄅ"}}, CodeInvalidReading}, // 英文詞沒有讀音
		{Entry{"ab c", nil}, CodeInvalidWord},
		{Entry{"a+b", nil}, CodeInvalidWord},
		{Entry{"123", nil}, CodeInvalidWord},
		{Entry{"世a", []string{"ㄕˋ ㄚ"}}, CodeInvalidWord},
		{Entry{strings.Repeat("a", 33), nil}, CodeInvalidWord},
		{Entry{"ネオ", []string{"ㄋㄟ ㄛ"}}, CodeInvalidWord},
		{Entry{strings.Repeat("好", 33), []string{strings.TrimSpace(strings.Repeat("ㄏㄠˇ ", 33))}}, CodeInvalidWord},
		{Entry{"世傑", []string{"ㄕˋ"}}, CodeInvalidReading},
		{Entry{"世傑", []string{"ㄕˋ ㄅㄩ"}}, CodeInvalidReading},
		{Entry{"世傑", nil}, CodeInvalidReading},
		{Entry{"了", []string{"ㄌㄜ˙", "ㄌㄧㄠˇ", "ㄌㄧㄠˋ", "ㄌㄧㄠˊ", "ㄌㄜˋ"}}, CodeLimitReadings},
	} {
		s := New()
		_, err := s.Add([]Entry{{"王小明", []string{"ㄨㄤˊ ㄒㄧㄠˇ ㄇㄧㄥˊ"}}, tc.e}, SourceManual, 1)
		if got := code(t, err); got != tc.code {
			t.Errorf("%+v：%s（%v）", tc.e, got, err)
		}
		if s.Len() != 0 || s.Version() != 0 {
			t.Errorf("%+v：整批拒絕，不能部分寫入", tc.e)
		}
	}
	if _, err := New().Add([]Entry{{strings.Repeat("好", 32), []string{strings.TrimSpace(strings.Repeat("ㄏㄠˇ ", 32))}}}, SourceManual, 1); err != nil {
		t.Fatalf("32 字可以：%v", err)
	}
	if _, err := New().Add([]Entry{{"呢", []string{"ㄋㄜ˙"}}}, SourceManual, 1); err != nil {
		t.Fatalf("輕聲：%v", err)
	}
}

func TestLimits(t *testing.T) {
	s := New()
	many := make([]Entry, MaxBatchItems+1)
	if code(t, func() error { _, err := s.Add(many, SourceAssistant, 1); return err }()) != CodeLimitBatch {
		t.Fatal("單批上限")
	}
	// 直接放滿到上限，確認再加會整批拒絕並回報剩餘額度。
	for i := 0; i < MaxWords; i++ {
		s.f.Words = append(s.f.Words, Word{Text: hanName(i), Readings: []string{"ㄨㄤˊ ㄒㄧㄠˇ"}})
	}
	s.reindex()
	_, err := s.Add([]Entry{{"王大明", []string{"ㄨㄤˊ ㄉㄚˋ ㄇㄧㄥˊ"}}}, SourceManual, 1)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeLimitWords || e.Left != 0 || e.Limit != MaxWords {
		t.Fatalf("%+v", err)
	}
	// 已有的詞加讀音不佔新名額。
	if _, err := s.Add([]Entry{{hanName(0), []string{"ㄨㄤˊ ㄒㄧㄠˋ"}}}, SourceManual, 1); err != nil {
		t.Fatal(err)
	}
}

func hanName(i int) string { return string(rune(0x4E00+i/100)) + string(rune(0x4E00+i%100)) }

func TestOverLimitFileKeptButNotUsed(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(`{"format":1,"words":[`)
	for i := 0; i < MaxWords+5; i++ {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, `{"word":%q,"readings":["ㄨㄤˊ ㄒㄧㄠˇ"]}`, hanName(i))
	}
	buf.WriteString(`,{"word":"bad","readings":["ㄅ"]}]}`)
	s, err := Load(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != MaxWords || s.Stored() != MaxWords+6 || len(s.Problems()) == 0 {
		t.Fatalf("len %d stored %d problems %v", s.Len(), s.Stored(), s.Problems())
	}
	if _, ok := s.Lookup(hanName(MaxWords + 1)); ok {
		t.Fatal("超過上限的詞不使用")
	}
	if code(t, func() error {
		_, err := s.Add([]Entry{{"王大明", []string{"ㄨㄤˊ ㄉㄚˋ ㄇㄧㄥˊ"}}}, SourceManual, 1)
		return err
	}()) != CodeOverLimit {
		t.Fatal("超過上限期間拒絕新增")
	}
	if _, err := s.Remove([]string{hanName(0)}, SourceManual, 2); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := s.Save(&out); err != nil {
		t.Fatal(err)
	}
	saved := out.String()
	back, err := Load(&out)
	if err != nil || back.Stored() != MaxWords+5 || !strings.Contains(saved, `"bad"`) {
		t.Fatalf("寫回時保留超出與無效的部分：%d %v", back.Stored(), err)
	}
	if _, ok := back.Lookup(hanName(MaxWords)); !ok {
		t.Fatal("刪掉一個後，原本超出的第一個詞進入上限內")
	}
}

func TestRemoveAndUndo(t *testing.T) {
	s := New()
	add, _ := s.Add([]Entry{{"陳怡君", []string{"ㄔㄣˊ ㄧˊ ㄐㄩㄣ"}}, {"林世傑", []string{"ㄌㄧㄣˊ ㄕˋ ㄐㄧㄝˊ"}}}, SourceAssistant, 1)
	if code(t, func() error { _, err := s.Remove([]string{"不存在"}, SourceManual, 2); return err }()) != CodeNotFound {
		t.Fatal("刪不存在的詞")
	}
	rm, err := s.Remove([]string{"陳怡君"}, SourceManual, 2)
	if err != nil || s.Len() != 1 {
		t.Fatal(err)
	}
	if _, err := s.Undo(rm.ID, SourceManual, 3); err != nil || s.Len() != 2 {
		t.Fatalf("復原刪除：%v", err)
	}
	if code(t, func() error { _, err := s.Undo(rm.ID, SourceManual, 3); return err }()) != CodeConflict {
		t.Fatal("同一批不能復原兩次")
	}
	// AI 一次加的詞整批撤回；其中一個後來被改過讀音就整批拒絕。
	if _, err := s.SetReadings("林世傑", []string{"ㄌㄧㄣˊ ㄕˋ ㄐㄧㄝˊ", "ㄌㄧㄣˊ ㄕˋ ㄐㄧㄝˋ"}, SourceManual, 4); err != nil {
		t.Fatal(err)
	}
	if code(t, func() error { _, err := s.Undo(add.ID, SourceManual, 5); return err }()) != CodeConflict || s.Len() != 2 {
		t.Fatal("後來改過的詞不能被整批復原蓋掉")
	}
	last := s.Batches()[0]
	if last.Op != "readings" {
		t.Fatalf("最新的批次在前：%+v", last)
	}
	if _, err := s.Undo(last.ID, SourceManual, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Undo(add.ID, SourceManual, 7); err != nil || s.Len() != 0 {
		t.Fatalf("讀音改回來後可以整批撤回：%v %d", err, s.Len())
	}
}

func TestBatchesKeptAndIDsMonotonic(t *testing.T) {
	s := New()
	for i := 0; i < KeepBatches+10; i++ {
		if _, err := s.Add([]Entry{{hanName(i), []string{"ㄨㄤˊ ㄒㄧㄠˇ"}}}, SourceManual, int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	bs := s.Batches()
	if len(bs) != KeepBatches || bs[0].ID != KeepBatches+10 || s.Len() != KeepBatches+10 {
		t.Fatalf("%d %d", len(bs), bs[0].ID)
	}
	if code(t, func() error { _, err := s.Undo(1, SourceManual, 0); return err }()) != CodeNotFound {
		t.Fatal("太舊的批次不能整批復原，但詞還在")
	}
}

func TestPhrases(t *testing.T) {
	s := New()
	b, err := s.AddPhrases([]string{"——", "台北市信義區", "……"}, SourceManual, 1)
	if err != nil || len(s.Phrases()) != 3 || s.Phrases()[0].ID != 1 {
		t.Fatalf("%+v %v", b, err)
	}
	if _, err := s.MovePhrase(3, 0, SourceManual, 2); err != nil || s.Phrases()[0].Text != "……" {
		t.Fatal(err)
	}
	rm, err := s.RemovePhrases([]uint64{2}, SourceManual, 3)
	if err != nil || len(s.Phrases()) != 2 {
		t.Fatal(err)
	}
	if _, err := s.AddPhrases([]string{"新的"}, SourceManual, 4); err != nil || s.Phrases()[2].ID != 4 {
		t.Fatalf("刪掉的編號不重用：%+v", s.Phrases())
	}
	if _, err := s.Undo(rm.ID, SourceManual, 5); err != nil || len(s.Phrases()) != 4 || s.Phrases()[3].Text != "台北市信義區" {
		t.Fatal(err)
	}
	if code(t, func() error {
		_, err := s.AddPhrases([]string{strings.Repeat("字", MaxPhraseRunes+1)}, SourceManual, 6)
		return err
	}()) != CodeInvalidWord {
		t.Fatal("片語長度")
	}
	if code(t, func() error { _, err := s.AddPhrases([]string{"  "}, SourceManual, 6); return err }()) != CodeEmpty {
		t.Fatal("空片語")
	}
}

func TestSaveLoadRoundTripAndNewerFormat(t *testing.T) {
	s := New()
	s.Add([]Entry{{armstrong, []string{armstrongReading}}}, SourceCompose, 9)
	s.AddPhrases([]string{"地址"}, SourceManual, 10)
	var buf bytes.Buffer
	if err := s.Save(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := Load(bytes.NewReader(buf.Bytes()))
	if err != nil || back.Len() != 1 || len(back.Phrases()) != 1 || back.Version() != 2 || len(back.Batches()) != 2 {
		t.Fatalf("%v", err)
	}
	if m := back.Exact(zhuyin.ParseReading(armstrongReading)); len(m) != 1 {
		t.Fatal("讀回後索引重建")
	}
	_, err = Load(strings.NewReader(`{"format":2,"words":[]}`))
	var newer *NewerFormatError
	if !errors.As(err, &newer) || newer.Format != 2 {
		t.Fatalf("較新格式：%v", err)
	}
}

func TestCloneIsIndependent(t *testing.T) {
	s := New()
	s.Add([]Entry{{"陳怡君", []string{"ㄔㄣˊ ㄧˊ ㄐㄩㄣ"}}}, SourceManual, 1)
	c := s.Clone()
	s.SetReadings("陳怡君", []string{"ㄔㄣˊ ㄧˊ ㄐㄩㄣˋ"}, SourceManual, 2)
	if w, _ := c.Lookup("陳怡君"); w.Readings[0] != "ㄔㄣˊ ㄧˊ ㄐㄩㄣ" || c.Version() != 1 {
		t.Fatalf("複本不受之後的修改影響：%+v", w)
	}
}

// 命令列 add - 與本機 API 用的 JSON 形狀。
func TestEntryJSON(t *testing.T) {
	var es []Entry
	if err := json.Unmarshal([]byte(`[{"word":"陳怡君","readings":["ㄔㄣˊ ㄧˊ ㄐㄩㄣ"]}]`), &es); err != nil || len(es) != 1 || es[0].Text != "陳怡君" || es[0].Readings[0] != "ㄔㄣˊ ㄧˊ ㄐㄩㄣ" {
		t.Fatalf("%+v %v", es, err)
	}
}

// 產品品質回歸用的 500 筆詞（docs/personal-vocabulary.md#驗收）要能完整載入。
func TestQualityFixtureLoads(t *testing.T) {
	f, err := os.Open("../../testdata/vocab/fixture-500.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s, err := Load(f)
	if err != nil || s.Len() != 500 || len(s.Problems()) != 0 {
		t.Fatalf("%d %v %v", s.Len(), s.Problems(), err)
	}
}

// #146：英文詞（混打時照字面送出的指令、工具名）。沒有讀音，大小寫照字面。
func TestEnglishWords(t *testing.T) {
	s := New()
	b, err := s.Add([]Entry{{"ls", nil}, {" kubectl ", []string{""}}, {"git-lfs", nil}, {"LS", nil}, {"a.b_c-1", nil}, {"世傑", []string{"ㄕˋ ㄐㄧㄝˊ"}}}, SourceManual, 5)
	if err != nil || b == nil || len(b.Changes) != 6 {
		t.Fatalf("%+v %v", b, err)
	}
	for _, w := range []string{"ls", "kubectl", "git-lfs", "LS", "a.b_c-1"} {
		if !s.HasEnglish(w) {
			t.Errorf("%s 要是英文詞", w)
		}
		got, ok := s.Lookup(w)
		if !ok || !got.English() || got.Readings == nil || len(got.Readings) != 0 {
			t.Errorf("%s：%+v", w, got)
		}
	}
	for _, w := range []string{"Ls", "lS", "l", "kube", "世傑"} {
		if s.HasEnglish(w) {
			t.Errorf("%s 不是英文詞（大小寫照字面、要完全相同）", w)
		}
	}
	if !s.HasEnglishPrefix("kube") || !s.HasEnglishPrefix("git-") || s.HasEnglishPrefix("lsx") || s.HasEnglishPrefix("") {
		t.Error("HasEnglishPrefix")
	}
	if s.Len() != 6 || s.EnglishLen() != 5 || s.Longest() != 2 || len(s.Problems()) != 0 {
		t.Fatalf("len %d english %d longest %d %v", s.Len(), s.EnglishLen(), s.Longest(), s.Problems())
	}
	if m := s.Exact(zhuyin.ParseReading("ㄕˋ ㄐㄧㄝˊ")); len(m) != 1 {
		t.Fatal("漢字詞照常進讀音索引")
	}
	if b, err := s.Add([]Entry{{"ls", nil}}, SourceManual, 6); b != nil || err != nil {
		t.Fatalf("重複加入沒有變更：%+v %v", b, err)
	}
	if _, err := s.SetReadings("ls", []string{"ㄌ ㄙ"}, SourceManual, 7); code(t, err) != CodeInvalidReading {
		t.Fatal("英文詞不能設讀音")
	}
	rm, err := s.Remove([]string{"ls"}, SourceManual, 8)
	if err != nil || s.HasEnglish("ls") || !s.HasEnglish("LS") {
		t.Fatalf("刪除：%v", err)
	}
	if _, err := s.Undo(rm.ID, SourceManual, 9); err != nil || !s.HasEnglish("ls") {
		t.Fatalf("復原：%v", err)
	}
	var buf bytes.Buffer
	s.Save(&buf)
	if !strings.Contains(buf.String(), "\"word\": \"ls\",\n   \"readings\": []") {
		t.Fatalf("英文詞存在 words、readings 是 []：\n%s", buf.String())
	}
	back, err := Load(&buf)
	if err != nil || !back.HasEnglish("kubectl") || back.Len() != 6 {
		t.Fatalf("讀回：%v", err)
	}
}

// legacyFile／legacyWord 是加入英文詞前（格式 1）的 vocab.json 結構，照當時的 vocab.go 凍結。
type legacyWord struct {
	Text     string   `json:"word"`
	Readings []string `json:"readings"`
	Source   string   `json:"source,omitempty"`
	Added    int64    `json:"added,omitempty"`
	Batch    uint64   `json:"batch,omitempty"`
}

type legacyFile struct {
	Format  int             `json:"format"`
	Version uint64          `json:"version"`
	Words   []legacyWord    `json:"words"`
	Phrases []Phrase        `json:"phrases"`
	Batches json.RawMessage `json:"batches"`
}

// 資料格式相容（#146）：英文詞不調高 Format。加入英文詞前的版本（main 的 0.2-dev 寫入者）
// 讀到含英文詞的檔案時，格式號碼相同所以照常讀入；英文詞在它的 Normalize 裡是「含非漢字」的無效詞，
// 只是不使用、原樣保留（TestOverLimitFileKeptButNotUsed 的同一條規則），寫回時沒有欄位會遺失。
// 反過來，舊版寫的檔案（只有漢字詞）新版照常讀。
func TestEnglishWordsReadableByFormat1Readers(t *testing.T) {
	s := New()
	s.Add([]Entry{{"世傑", []string{"ㄕˋ ㄐㄧㄝˊ"}}, {"ls", nil}, {"kubectl", nil}}, SourceManual, 1)
	var buf bytes.Buffer
	s.Save(&buf)
	var old legacyFile
	if err := json.Unmarshal(buf.Bytes(), &old); err != nil || old.Format != 1 || len(old.Words) != 3 {
		t.Fatalf("格式 1 的讀取器要能整檔讀入：%+v %v", old, err)
	}
	// 舊版的檢查：英文詞含非漢字，不使用（不進索引），但留在 Words 裡；也不能帶讀音，舊版才不會誤用。
	for _, w := range old.Words {
		han := true
		for _, r := range w.Text {
			han = han && zhuyin.IsHan(r)
		}
		if !han && len(w.Readings) != 0 {
			t.Fatalf("英文詞不能帶讀音：%+v", w)
		}
	}
	// 舊版寫回：用它的結構重新編碼，新版讀回時英文詞還在。
	again, _ := json.Marshal(old)
	back, err := Load(bytes.NewReader(again))
	if err != nil || !back.HasEnglish("ls") || !back.HasEnglish("kubectl") || back.Len() != 3 {
		t.Fatalf("舊版寫回後英文詞要還在：%v", err)
	}
	// readings 欄位不存在或是 null 也一樣。
	legacy := `{"format":1,"version":1,"words":[{"word":"ls","readings":null},{"word":"rm"},{"word":"世傑","readings":["ㄕˋ ㄐㄧㄝˊ"]}],"phrases":[],"batches":[]}`
	back, err = Load(strings.NewReader(legacy))
	if err != nil || !back.HasEnglish("ls") || !back.HasEnglish("rm") || back.Len() != 3 {
		t.Fatalf("%v %v", err, back.Problems())
	}
	if w, _ := back.Lookup("rm"); w.Readings == nil {
		t.Fatal("列出時 readings 是 []，不是 null")
	}
}
