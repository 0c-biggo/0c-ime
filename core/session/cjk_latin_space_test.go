package session

import (
	"encoding/json"
	"testing"

	"github.com/0c-biggo/0c-ime/core/inputstyle"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/vocab"
)

// #273（GitHub 0c-ime#58）：設定「中英文之間自動加空格」只在同一次送出的文字裡，漢字與 ASCII 英文字母直接相連時
// 補一個半形空格。全形標點、已有空白、數字、換行旁不補；英文字內部與大小寫不動。
func TestSpaceCJKLatinRules(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// issue 的範例
		{"今天用VS Code寫Python", "今天用 VS Code 寫 Python"},
		{"用ChatGPT整理資料", "用 ChatGPT 整理資料"},
		{"API文件", "API 文件"},
		{"今天用 Python 寫程式", "今天用 Python 寫程式"},
		{"我用Python，接著測試", "我用 Python，接著測試"},
		// 已有空白只補缺的一邊；全形空白也算空白
		{"用 Python寫", "用 Python 寫"},
		{"用　Python　寫", "用　Python　寫"},
		// 全形標點、半形標點旁不補
		{"「Python」", "「Python」"},
		{"（API）文件", "（API）文件"},
		{"用C++寫", "用 C++寫"},
		{"用.NET寫", "用.NET 寫"},
		// 數字先不處理（L1）：英文後面接數字、數字接漢字都不補
		{"用Python3寫", "用 Python3寫"},
		{"3月", "3月"},
		{"第2版", "第2版"},
		// 英文字內部、大小寫照舊；換行旁不補
		{"iPhone手機", "iPhone 手機"},
		{"我\nOK", "我\nOK"},
		{"OK\n我", "OK\n我"},
		// 全形英數、注音符號、假名、Emoji 不是漢字或 ASCII 字母
		{"用Ｐｙｔｈｏｎ寫", "用Ｐｙｔｈｏｎ寫"},
		{"我ㄒA", "我ㄒA"},
		{"テストA", "テストA"},
		{"😀A", "😀A"},
		// 擴充區的漢字也算
		{"𠀋A", "𠀋 A"},
		// 句首、句尾不加
		{"A", "A"}, {"我", "我"}, {"", ""},
		// 簡體字一樣是漢字
		{"用VS Code写Python", "用 VS Code 写 Python"},
	} {
		if got := spaceCJKLatin(tc.in); got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.in, got, tc.want)
		}
	}
}

func spaceSession(on bool) *Session {
	s := englishBoundarySession()
	s.CJKLatinSpace = on
	s.CtrlPunct = true
	return s
}

// 中英混打：組字區照打的顯示，送出時才補；關閉時跟以前一樣。
func TestCJKLatinSpaceInMixedInput(t *testing.T) {
	for _, tc := range []struct{ keys, off, on string }{
		{"hellosu3cl3", "hello你好", "hello 你好"},
		{"ji3hellosu3", "我hello你", "我 hello 你"},
		{"ji3hello su3", "我hello 你", "我 hello 你"},
		{"ji3hello worldsu3", "我hello world你", "我 hello world 你"},
		{"apisu3", "api你", "api 你"},
		{"ji3", "我", "我"},
		{"hello", "hello", "hello"},
	} {
		for _, on := range []bool{false, true} {
			s := spaceSession(on)
			snap := mixedType(s, tc.keys)
			if snap.Text() != tc.off {
				t.Fatalf("%q 開關 %v：組字區照打的顯示 %q，要 %q", tc.keys, on, snap.Text(), tc.off)
			}
			want := map[bool]string{false: tc.off, true: tc.on}[on]
			if got := key(s, proto.KeyEnter).Commit; got != want {
				t.Errorf("%q 開關 %v：送出 %q，要 %q", tc.keys, on, got, want)
			}
		}
	}
}

// 全形標點旁不補：Ctrl＋, 打的「，」與 Shift＋, 的全形逗號。
func TestCJKLatinSpaceSkipsFullWidthPunctuation(t *testing.T) {
	for _, comma := range []string{proto.KeyCtrlComma, "<"} {
		s := spaceSession(true)
		mixedType(s, "ji3hello")
		key(s, comma)
		mixedType(s, "su3")
		if got := key(s, proto.KeyEnter).Commit; got != "我 hello，你" {
			t.Errorf("%s：送出 %q", comma, got)
		}
	}
}

// 一般模式（沒開中英混打）：組字中切到英文打的字母跟組字一起送出，交界一樣補。
func TestCJKLatinSpaceWithEnglishModeInsideComposition(t *testing.T) {
	for _, on := range []bool{false, true} {
		s := newSession()
		s.CJKLatinSpace = on
		typeSyl(s, "ㄨㄛˇ")
		s.Handle(toEnglish)
		mixedType(s, "API")
		s.Handle(toEnglish)
		typeSyl(s, "ㄒㄧㄤˇ")
		want := map[bool]string{false: "我API想", true: "我 API 想"}[on]
		if snap := key(s, proto.KeyEnter); snap.Commit != want {
			t.Errorf("開關 %v：送出 %q，要 %q", on, snap.Commit, want)
		}
	}
	// 沒在組字時英文直接交給應用程式，引擎看不到，不補（L1）。
	s := newSession()
	s.CJKLatinSpace = true
	s.Handle(toEnglish)
	if snap := key(s, "a"); snap.Handled || snap.Commit != "" {
		t.Fatalf("英文模式沒在組字時照舊交還應用程式：%+v", snap)
	}
}

// 英文模式開了自動補字：組字中切過去打的英文字跟前面的組字一起送出，也補。
func TestCJKLatinSpaceWithEnglishAutocomplete(t *testing.T) {
	s := spaceSession(true)
	s.MixedInput = false
	s.EnglishAutocomplete = true
	typeSyl(s, "ㄨㄛˇ")
	s.Handle(toEnglish)
	mixedType(s, "hello")
	if got := key(s, proto.KeyEnter).Commit; got != "我 hello" {
		t.Fatalf("送出 %q", got)
	}
}

// Shift＋字母送出組字再打那個字母（風格「Shift＋字母：送出組字」）是同一次送出，交界也補。
func TestCJKLatinSpaceWithShiftLetterCommit(t *testing.T) {
	s := customStyle(func(r *inputstyle.Rules) { r.ShiftLetter = "commit" })
	s.CJKLatinSpace = true
	typeSyl(s, "ㄨㄛˇ")
	if snap := key(s, "A"); !snap.Handled || snap.Commit != "我 A" {
		t.Fatalf("Shift＋A：%+v", snap)
	}
}

// 輸出簡體字：轉換後的漢字一樣補，讀音決定的簡體寫法不受影響。
func TestCJKLatinSpaceWithSimplifiedOutput(t *testing.T) {
	for _, on := range []bool{false, true} {
		s, _ := simplifiedSession(t, true)
		s.CJKLatinSpace = on
		typeReading(s, "ㄐㄧˋ ㄏㄨㄚˋ")
		s.Handle(toEnglish)
		mixedType(s, "OK")
		s.Handle(toEnglish)
		typeReading(s, "ㄊㄞˊ ㄨㄢ")
		want := map[bool]string{false: "计划OK台湾", true: "计划 OK 台湾"}[on]
		if got := key(s, proto.KeyEnter).Commit; got != want {
			t.Errorf("開關 %v：送出 %q，要 %q", on, got, want)
		}
	}
}

// 私人欄位（密碼等）照原樣送出。
func TestCJKLatinSpaceSkipsPrivateFields(t *testing.T) {
	s := spaceSession(true)
	for _, k := range "ji3hello" {
		s.Handle(proto.Request{Type: proto.Key, Key: string(k), Private: true})
	}
	if got := s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEnter, Private: true}).Commit; got != "我hello" {
		t.Fatalf("私人欄位：%q", got)
	}
	// 下一段不是私人欄位就照設定補。
	mixedType(s, "ji3hello")
	if got := key(s, proto.KeyEnter).Commit; got != "我 hello" {
		t.Fatalf("回到一般欄位：%q", got)
	}
}

// 學習跟沒開時完全一樣（用補空格前的文字），前文確認照常：外殼比對的是補過空格的 Commit，引擎記的末詞不變。
func TestCJKLatinSpaceKeepsLearningAndDocumentContext(t *testing.T) {
	var stores [2]string
	for i, on := range []bool{false, true} {
		s := spaceSession(on)
		s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
		mixedType(s, "ji3hellosu3cl3")
		snap := key(s, proto.KeyEnter)
		want := map[bool]string{false: "我hello你好", true: "我 hello 你好"}[on]
		if snap.Commit != want || snap.ContextID == 0 || s.pendingLeft != "你好" {
			t.Fatalf("開關 %v：%q 前文代號 %d 末詞 %q", on, snap.Commit, snap.ContextID, s.pendingLeft)
		}
		b, _ := json.Marshal(s.model.Store)
		stores[i] = string(b)
		s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: snap.ContextID})
		if s.documentLeft != "你好" {
			t.Fatalf("開關 %v：確認後的前文 %q", on, s.documentLeft)
		}
	}
	if stores[0] != stores[1] {
		t.Fatalf("補空格不能改變學到的東西：\n%s\n%s", stores[0], stores[1])
	}
}

// 符號表直接送出的組字（含換行的片語連同前面的組字）跟 Enter 送出一樣補，同輸出簡體字；片語內部的換行旁不補。
func TestCJKLatinSpaceWithPaletteDirectCommit(t *testing.T) {
	s := paletteSession()
	s.Preview02 = true
	s.CJKLatinSpace = true
	if _, err := s.model.VocabAdd([]vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}, vocab.SourceManual); err != nil {
		t.Fatal(err) // 建立詞彙集（同 TestPalettePhrasesTab）
	}
	if _, err := s.model.Vocab.AddPhrases([]string{"OK\n好"}, vocab.SourceManual, 0); err != nil {
		t.Fatal(err)
	}
	typeSyl(s, "ㄨㄛˇ")
	key(s, "=")
	if snap := key(s, proto.KeyBackTab); snap.Candidates == nil || snap.Candidates.Kind != "phrases" {
		t.Fatalf("我的片語：%+v", snap.Candidates)
	}
	if snap := key(s, "1"); snap.Commit != "我 OK\n好" {
		t.Fatalf("含換行的片語連同組字直接提交：%+v", snap)
	}
}
