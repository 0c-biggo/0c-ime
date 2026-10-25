package settings

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/core/fuzzy"
)

func TestDefaultsMissingFieldsAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := Path(filepath.Join(dir, "user.json"))
	if s, err := Load(path); err != nil || s != freshSettings() || s.Selection != Neural || s.Punctuation || !s.DocumentContext {
		t.Fatalf("缺檔應回預設值：%+v %v", s, err)
	}
	os.WriteFile(path, []byte(`{"selection":"statistical"}`), 0o600)
	s, err := Load(path)
	if err != nil || s.Selection != Statistical || s.Punctuation || !s.DocumentContext {
		t.Fatalf("缺漏欄位應沿用預設值：%+v %v", s, err)
	}
	if err = s.Set("punctuation", "on"); err != nil {
		t.Fatal(err)
	}
	if err = Save(path, s); err != nil {
		t.Fatal(err)
	}
	if back, _ := Load(path); back != s {
		t.Fatalf("來回不一致：%+v", back)
	}
}

func TestPunctuationKeepsExplicitPreference(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name, body := "off", `{"schema":1,"punctuation":false}`
		if enabled {
			name, body = "on", `{"schema":1,"punctuation":true}`
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), FileName)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			s, err := Load(path)
			if err != nil || s.Punctuation != enabled {
				t.Fatalf("既有補標點選擇應保留：%+v %v", s, err)
			}
			if b, err := os.ReadFile(path); err != nil || string(b) != body {
				t.Fatalf("讀取不得改寫既有設定：%s %v", b, err)
			}
			if err := s.Set("selection", Statistical); err != nil {
				t.Fatal(err)
			}
			if err := Save(path, s); err != nil {
				t.Fatal(err)
			}
			if back, err := Load(path); err != nil || back.Punctuation != enabled || back.Selection != Statistical {
				t.Fatalf("改其他設定不得重設補標點：%+v %v", back, err)
			}
		})
	}
}

// 較新版本寫入的選字模式本版不認得：只有 selection 退回預設，其他欄位照常讀取，不算錯誤。
func TestUnknownSelectionOnlyResetsThatField(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	os.WriteFile(path, []byte(`{"selection":"remote","punctuation":false,"document_context":false}`), 0o600)
	r, err := Read(path)
	if err != nil || r.State != StateOK {
		t.Fatalf("單一欄位的未知值不是錯誤：%+v %v", r, err)
	}
	if s := r.Settings; s.Selection != Neural || s.Punctuation || s.DocumentContext {
		t.Fatalf("selection 退回預設，其餘照常讀取：%+v", s)
	}
	if len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "selection") || !strings.Contains(r.Notes[0], `"remote"`) {
		t.Fatalf("應說明哪個欄位退回預設：%q", r.Notes)
	}
}

func TestRejectsUnknownValues(t *testing.T) {
	s := Default()
	for _, c := range [][2]string{{"selection", "x"}, {"punctuation", "yes"}, {"nope", "on"}} {
		if s.Set(c[0], c[1]) == nil {
			t.Fatalf("應拒絕 %v", c)
		}
	}
	if s != Default() {
		t.Fatal("拒絕的設定不應改變內容")
	}
}

// 缺少模糊音設定的舊檔與全新安裝四組都是預設 select，存檔也不憑空加欄位（F35）。
func TestFuzzyDefaultsSelectAndLeavesFileUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	os.WriteFile(path, []byte(`{"selection":"neural","punctuation":true,"document_context":true}`), 0o600)
	s, err := Load(path)
	if err != nil || s.FuzzyConfig().Key() != "z-zh=select,c-ch=select,s-sh=select,en-eng=select" {
		t.Fatalf("舊設定檔應四組都是 select：%q %v", s.FuzzyConfig().Key(), err)
	}
	if Default().FuzzyConfig() != s.FuzzyConfig() {
		t.Fatal("全新安裝與缺欄位的舊檔應相同")
	}
	if err = Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "fuzzy") {
		t.Fatalf("維持預設時不應寫入欄位：%s", b)
	}
}

func TestFuzzySetLoadAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s := Default()
	for _, p := range fuzzy.Pairs() {
		if err := s.Set("fuzzy-"+p.ID(), "select"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Set("fuzzy-s-sh", "off"); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil || back != s {
		t.Fatalf("來回不一致：%+v %v", back, err)
	}
	c := back.FuzzyConfig()
	want := map[fuzzy.Pair]fuzzy.Mode{fuzzy.ZZh: fuzzy.Select, fuzzy.CCh: fuzzy.Select, fuzzy.SSh: fuzzy.Off, fuzzy.EnEng: fuzzy.Select}
	for p, m := range want {
		if c.Mode(p) != m {
			t.Errorf("%s 應為 %s，實際 %s", p.ID(), m, c.Mode(p))
		}
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"s-sh": "off"`) || strings.Contains(string(b), "z-zh") {
		t.Errorf("關閉要明確寫入，維持預設 select 的配對不留欄位：%s", b)
	}
}

// 較新版本寫入本版不認得的值：只有那一組當作關閉，其他設定照常讀取，存檔時原值保留。
func TestFuzzyUnknownValueOnlyDisablesThatPair(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	os.WriteFile(path, []byte(`{"selection":"statistical","fuzzy":{"z-zh":"learn","c-ch":"select"}}`), 0o600)
	s, err := Load(path)
	if err != nil || s.Selection != Statistical {
		t.Fatalf("其他欄位照常讀取：%+v %v", s, err)
	}
	c := s.FuzzyConfig()
	if c.Mode(fuzzy.ZZh) != fuzzy.Off || c.Mode(fuzzy.CCh) != fuzzy.Select {
		t.Fatalf("z-zh 不認得的值視為關閉，c-ch 照常：%q", c.Key())
	}
	if err = s.Set("punctuation", "off"); err != nil {
		t.Fatal(err)
	}
	if err = Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"learn"`) {
		t.Fatalf("存檔不得丟掉較新版本寫入的值：%s", b)
	}
}

// 較新版本寫入不是字串的值（物件、布林）或型別不同的整個 fuzzy 欄位：不拒收整份設定檔，
// 只有那一組（或全部）視為關閉；其他設定照常讀取，存檔時非字串的原值保留。
func TestFuzzyTolerantOfNonStringValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	os.WriteFile(path, []byte(`{"selection":"statistical","punctuation":false,"fuzzy":{"z-zh":{"mode":"auto"},"s-sh":true,"c-ch":"select"}}`), 0o600)
	s, err := Load(path)
	if err != nil || s.Selection != Statistical || s.Punctuation {
		t.Fatalf("其他設定應照常讀取：%+v %v", s, err)
	}
	if c := s.FuzzyConfig(); c.Key() != "c-ch=select,en-eng=select" {
		t.Fatalf("不認得的值視為關閉，缺漏的照預設：%q", c.Key())
	}
	if err = Save(path, s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"mode": "auto"`) && !strings.Contains(string(b), `"mode":"auto"`) || !strings.Contains(string(b), "true") {
		t.Fatalf("存檔應原樣保留較新版本的值：%s", b)
	}
	// 整個 fuzzy 欄位型別不對：視為全部關閉，仍可讀，存檔時原文保留。
	os.WriteFile(path, []byte(`{"selection":"statistical","fuzzy":"later"}`), 0o600)
	if s, err = Load(path); err != nil || s.Selection != Statistical || s.FuzzyConfig().Enabled() {
		t.Fatalf("型別不對的 fuzzy 欄位不得拒收設定檔：%+v %v", s, err)
	}
	if err = s.Set("punctuation", "off"); err != nil {
		t.Fatal(err)
	}
	if err = Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"fuzzy": "later"`) {
		t.Fatalf("存檔不得丟掉型別不同的 fuzzy 欄位：%s", b)
	}
}

// 較新版本新增的配對（本版沒有）原文保留；明確設定某一組時，只改那一組。
func TestFuzzyKeepsUnknownPairsAndEditsOnlyTheNamedOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	os.WriteFile(path, []byte(`{"fuzzy":{"an-ang":"select","z-zh":{"mode":"auto"}}}`), 0o600)
	s, err := Load(path)
	if err != nil || s.FuzzyConfig().Key() != "c-ch=select,s-sh=select,en-eng=select" {
		t.Fatalf("讀不懂的 z-zh 關閉，其餘照預設：%q %v", s.FuzzyConfig().Key(), err)
	}
	if err = s.Set("fuzzy-c-ch", "auto"); err != nil {
		t.Fatal(err)
	}
	if err = Save(path, s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{`"an-ang": "select"`, `"mode": "auto"`, `"c-ch": "auto"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("存檔應包含 %s：%s", want, b)
		}
	}
	// 明確設定被保留原文的那一組：改成本版認得的值，原文不再保留。
	back, _ := Load(path)
	if err = back.Set("fuzzy-z-zh", "off"); err != nil {
		t.Fatal(err)
	}
	if err = Save(path, back); err != nil {
		t.Fatal(err)
	}
	if b, _ = os.ReadFile(path); strings.Contains(string(b), `"mode"`) || !strings.Contains(string(b), `"an-ang"`) {
		t.Errorf("明確設定 z-zh 後只該丟掉它自己的舊原文：%s", b)
	}
}

// 值裡出現任何字元都不影響存檔（沒有帶內標記）。
func TestFuzzyOddStringValueRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	os.WriteFile(path, []byte(`{"fuzzy":{"z-zh":"\u0000raw:{bad"}}`), 0o600)
	s, err := Load(path)
	if err != nil || s.FuzzyConfig().Mode(fuzzy.ZZh) != fuzzy.Off {
		t.Fatalf("%+v %v", s, err)
	}
	if err = Save(path, s); err != nil {
		t.Fatalf("存檔不得失敗：%v", err)
	}
	if back, err := Load(path); err != nil || back != s {
		t.Fatalf("來回不一致：%+v %v", back, err)
	}
}

func TestFuzzyRejectsBadSettings(t *testing.T) {
	s := Default()
	for _, c := range [][2]string{{"fuzzy-z-zh", "learn"}, {"fuzzy-z-zh", "on"}, {"fuzzy-f-h", "select"}, {"fuzzy-", "select"}} {
		if s.Set(c[0], c[1]) == nil {
			t.Errorf("應拒絕 %v", c)
		}
	}
	if s != Default() {
		t.Fatal("拒絕的設定不應改變內容")
	}
	if _, ok := Keys()["fuzzy-en-eng"]; !ok {
		t.Fatal("Keys 應列出模糊音設定")
	}
}

// 較新版本（同一個 schema 內）新增的欄位與已知欄位的新值：本版讀寫後都還在，只改使用者明確改的欄位。
func TestFutureFieldsAndValuesSurviveSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	os.WriteFile(path, []byte(`{"schema":1,"selection":"remote","punctuation":false,"sync":{"enabled":true},"zeta":[1,2]}`), 0o600)
	s, err := Load(path)
	if err != nil || s.Selection != Neural || s.Punctuation {
		t.Fatalf("%+v %v", s, err)
	}
	if err = s.Set("document-context", "off"); err != nil {
		t.Fatal(err)
	}
	if err = Save(path, s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{`"selection": "remote"`, `"punctuation": false`, `"document_context": false`, `"sync"`, `"enabled": true`, `"zeta"`, `"schema": 2`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("存檔應包含 %s：%s", want, b)
		}
	}
	if back, err := Load(path); err != nil || back != s {
		t.Fatalf("來回不一致：%+v %v", back, err)
	}
	// 明確改 selection：不再寫回舊的原文；其餘未知欄位仍保留。
	if err = s.Set("selection", "statistical"); err != nil {
		t.Fatal(err)
	}
	if err = Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ = os.ReadFile(path); strings.Contains(string(b), "remote") || !strings.Contains(string(b), `"selection": "statistical"`) || !strings.Contains(string(b), `"sync"`) {
		t.Fatalf("明確設定後只該丟掉 selection 的舊原文：%s", b)
	}
}

// 本版讀不懂的布林值同樣只影響那個欄位，原文保留到使用者明確設定為止。
func TestUnusableBoolValueIsKeptUntilSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	os.WriteFile(path, []byte(`{"punctuation":"maybe","selection":"statistical"}`), 0o600)
	s, err := Load(path)
	if err != nil || s.Punctuation || s.Selection != Statistical {
		t.Fatalf("punctuation 退回預設，其餘照常：%+v %v", s, err)
	}
	if err = Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"punctuation": "maybe"`) {
		t.Fatalf("存檔不得丟掉讀不懂的值：%s", b)
	}
	if err = s.Set("punctuation", "off"); err != nil {
		t.Fatal(err)
	}
	if err = Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "maybe") || !strings.Contains(string(b), `"punctuation": false`) {
		t.Fatalf("明確設定後應改寫成新值：%s", b)
	}
}

// schema 比本版新（或不是整數）：可以讀，但不覆寫，檔案內容不變。
func TestNewerSchemaIsReadOnly(t *testing.T) {
	for _, body := range []string{
		`{"schema":3,"selection":"statistical","future":"x"}`,
		`{"schema":"v2","selection":"statistical"}`,
	} {
		path := filepath.Join(t.TempDir(), FileName)
		os.WriteFile(path, []byte(body), 0o600)
		r, err := Read(path)
		if err != nil || !r.ReadOnly || !r.Settings.ReadOnly() || r.Settings.Selection != Statistical {
			t.Fatalf("%s：可讀、標記只讀：%+v %v", body, r, err)
		}
		s := r.Settings
		if err = s.Set("punctuation", "off"); !errors.Is(err, ErrReadOnly) {
			t.Fatalf("唯讀設定應拒絕修改：%v", err)
		}
		if err = Save(path, s); !errors.Is(err, ErrReadOnly) {
			t.Fatalf("%s：應拒絕覆寫：%v", body, err)
		}
		if b, _ := os.ReadFile(path); string(b) != body {
			t.Fatalf("被拒絕的存檔不得改變檔案：%s", b)
		}
	}
	// 目前的 schema 與缺少 schema 的舊檔都可寫。
	for _, body := range []string{`{"schema":1}`, `{"schema":0}`, `{}`} {
		path := filepath.Join(t.TempDir(), FileName)
		os.WriteFile(path, []byte(body), 0o600)
		r, err := Read(path)
		if err != nil || r.ReadOnly {
			t.Fatalf("%s：應可寫：%+v %v", body, r, err)
		}
		if err = Save(path, r.Settings); err != nil {
			t.Fatalf("%s：%v", body, err)
		}
	}
}

func TestSaveWritesSchemaFirstInStableOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := Save(path, Default()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	text := string(b)
	last := -1
	for _, key := range []string{`"schema": 2`, `"selection"`, `"punctuation"`, `"document_context"`} {
		at := strings.Index(text, key)
		if at <= last {
			t.Fatalf("%s 應依固定順序出現：%s", key, text)
		}
		last = at
	}
	if r, err := Read(path); err != nil || r.Schema != SchemaVersion || len(r.Notes) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
}

// 無法解析的檔案：回報錯誤與狀態，可改名保留後從預設值重來。
func TestCorruptFileIsReportedAndCanBeQuarantined(t *testing.T) {
	for _, body := range []string{"not json", `["selection"]`, `{"selection":`} {
		path := filepath.Join(t.TempDir(), FileName)
		os.WriteFile(path, []byte(body), 0o600)
		r, err := Read(path)
		if !errors.Is(err, ErrCorrupt) || r.State != StateCorrupt || r.Settings != Default() {
			t.Fatalf("%q：%+v %v", body, r, err)
		}
		if s, err := Load(path); err == nil || s != Default() {
			t.Fatalf("Load 應回預設值與錯誤：%+v %v", s, err)
		}
		moved, err := Quarantine(path, time.Date(2026, 10, 6, 1, 2, 3, 0, time.UTC))
		if err != nil || !strings.HasSuffix(moved, ".corrupt-20261006T010203Z") {
			t.Fatalf("%q：%v", moved, err)
		}
		if b, _ := os.ReadFile(moved); string(b) != body {
			t.Fatalf("改名保留的內容應不變：%s", b)
		}
		if r, err = Read(path); err != nil || r.State != StateMissing {
			t.Fatalf("改名後視為不存在：%+v %v", r, err)
		}
	}
}

func TestItemsListEveryKeyWithCurrentValue(t *testing.T) {
	s := Default()
	items := s.Items()
	want := 21 + len(fuzzy.Pairs()) + len(ruleSpecs()) // 含 simplified-hotkey（#192）、toneless（#150）
	if CapsLockEnglishSetting(runtime.GOOS) {
		want++ // caps-lock-english（#173）只有 Windows、Linux
	}
	if len(items) != want || items[0].Key != "selection" {
		t.Fatalf("項目數量或順序不對：%+v", items)
	}
	for _, it := range items {
		found := false
		for _, v := range it.Values {
			found = found || v == it.Default
		}
		if it.Type != "choice" || !found || it.Value != it.Default || it.Label == "" {
			t.Errorf("預設設定的每一項都應是預設值且有說明：%+v", it)
		}
		if got, ok := s.Value(it.Key); !ok || got != it.Value {
			t.Errorf("Value(%s) = %q %v", it.Key, got, ok)
		}
	}
	if err := s.Set("fuzzy-c-ch", "select"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Value("fuzzy-c-ch"); v != "select" {
		t.Fatalf("fuzzy-c-ch = %q", v)
	}
	if _, ok := s.Value("nope"); ok {
		t.Fatal("未知的鍵不應有值")
	}
}

func TestSetErrorsCarryMachineReadableCodes(t *testing.T) {
	s := Default()
	for _, c := range []struct{ key, value, code string }{
		{"nope", "on", CodeUnknownKey}, {"fuzzy-", "select", CodeUnknownKey},
		{"selection", "x", CodeInvalidValue}, {"punctuation", "yes", CodeInvalidValue}, {"fuzzy-z-zh", "learn", CodeInvalidValue},
	} {
		var se *SetError
		if err := s.Set(c.key, c.value); !errors.As(err, &se) || se.Code != c.code || se.Key != c.key {
			t.Errorf("%v：%v", c, err)
		}
	}
}

// auto 是合法值：寫入設定檔、讀回來是 auto，核心設定的 AutoEnabled 為真；off 仍不留欄位。
func TestFuzzyAutoRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s := Default()
	if err := s.Set("fuzzy-z-zh", "auto"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("fuzzy-c-ch", "select"); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c := back.FuzzyConfig()
	if c.Mode(fuzzy.ZZh) != fuzzy.Auto || c.Mode(fuzzy.CCh) != fuzzy.Select || !c.AutoEnabled() {
		t.Fatalf("設定 %q", c.Key())
	}
	if err := back.Set("fuzzy-z-zh", "off"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "auto") {
		t.Fatalf("前提：舊檔仍是 auto：%s", b)
	}
	if err := Save(path, back); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), `"auto"`) || !strings.Contains(string(b), `"z-zh": "off"`) {
		t.Fatalf("關回 off 要明確寫入、不留 auto：%s", b)
	}
	if err := back.Set("fuzzy-z-zh", "select"); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, back); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "z-zh") {
		t.Fatalf("回到預設 select 不得留下欄位：%s", b)
	}
}
