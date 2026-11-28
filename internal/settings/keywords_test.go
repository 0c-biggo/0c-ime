package settings

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// #363：設定程式的搜尋除了名稱、說明、選項名稱與提示，還比對每一項的關鍵字（Item.Keywords）。

// searchFold 把文字摺成搜尋比對用的樣子：全形英數與符號（U+FF01–FF5E）換成半形、不分大小寫。
// 設定程式的 ui/search.js 用同一套規則（fold）；這裡用來核對關鍵字有沒有跟其他文字重複。
func searchFold(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0xFF01 && r <= 0xFF5E {
			r -= 0xFEE0
		} else if r == 0x3000 {
			r = ' '
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// searchableTexts 是搜尋本來就會比對的文字：名稱、說明、選項名稱與選項提示；依平台不同的說明、名稱與提示三個平台都算。
func searchableTexts(it Item) []string {
	out := []string{it.Label, it.Description}
	for _, label := range it.ValueLabels {
		out = append(out, label)
	}
	for _, goos := range helpGOOS {
		for _, hint := range valueHints(goos, it.Key) {
			out = append(out, hint)
		}
		switch it.Key {
		case "simplified-hotkey":
			out = append(out, simplifiedHotkeyDescription(goos))
		case "ctrl-punctuation":
			out = append(out, ctrlPunctuationDescription(goos))
		case "zhuyin-error-hint":
			out = append(out, zhuyinErrorHintDescription(goos))
		case "mode-memory":
			out = append(out, modeNativeLabel(goos, false), modeNativeLabel(goos, true))
		}
	}
	return out
}

// allItems 是三個平台合起來的設定項目：這台機器的 Items()，加上只有別的平台才有的（macOS 沒有 Caps Lock 那一項）。
func allItems() []Item {
	items := Default().Items()
	for _, it := range items {
		if it.Key == "caps-lock-english" {
			return items
		}
	}
	return append(items, Item{Key: "caps-lock-english", Label: "Caps Lock 開著時直接打英文", Description: "Caps Lock 開著時暫時打英文，關掉回到注音。",
		Keywords: keywords("caps-lock-english")})
}

// 關鍵字是名稱、說明與選項裡沒有的詞：已經寫在那些文字裡的詞搜尋本來就找得到，列了只是重複。
// 每個不超過 8 個字、每項最多 6 個、同一項裡不重複、不含空白（搜尋用空白分開多個詞）。
func TestKeywordsAddWordsTheTextsLack(t *testing.T) {
	withKeywords := 0
	for _, it := range allItems() {
		if len(it.Keywords) > 6 {
			t.Errorf("%s 的關鍵字太多（%d 個，最多 6 個）：%v", it.Key, len(it.Keywords), it.Keywords)
		}
		if len(it.Keywords) > 0 {
			withKeywords++
		}
		seen := map[string]bool{}
		for _, word := range it.Keywords {
			folded := searchFold(word)
			if n := utf8.RuneCountInString(word); n == 0 || n > 8 {
				t.Errorf("%s 的關鍵字 %q 要是 1 到 8 個字", it.Key, word)
			}
			if strings.ContainsAny(word, " \t\n　") {
				t.Errorf("%s 的關鍵字 %q 不能有空白", it.Key, word)
			}
			if seen[folded] {
				t.Errorf("%s 的關鍵字 %q 重複", it.Key, word)
			}
			seen[folded] = true
			if strings.Contains(searchFold(it.Label), folded) {
				t.Errorf("%s 的關鍵字 %q 名稱「%s」裡已經有了", it.Key, word, it.Label)
				continue
			}
			for _, text := range searchableTexts(it) {
				if strings.Contains(searchFold(text), folded) {
					t.Errorf("%s 的關鍵字 %q 已經寫在說明或選項裡，不必列：%q", it.Key, word, text)
					break
				}
			}
		}
		b, err := json.Marshal(it)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"keywords"`) != (len(it.Keywords) > 0) {
			t.Errorf("%s：沒有關鍵字時不帶 keywords：%s", it.Key, b)
		}
	}
	if withKeywords == 0 {
		t.Fatal("沒有任何一項帶 keywords")
	}
}

// Items() 帶的就是 keywords(鍵名)；不認得的鍵沒有關鍵字。
func TestKeywordsComeFromTheKey(t *testing.T) {
	for _, it := range Default().Items() {
		if got, want := strings.Join(it.Keywords, "、"), strings.Join(keywords(it.Key), "、"); got != want {
			t.Errorf("%s 的 keywords：%q，應是 %q", it.Key, got, want)
		}
	}
	if len(keywords("no-such-setting")) != 0 {
		t.Error("不認得的鍵沒有關鍵字")
	}
}

// 使用者實際會打的詞（issue #363 列的，加上別的輸入法的叫法）：每個詞要在指定那一項的名稱、說明、選項、提示或
// 關鍵字裡找得到。拿掉某個關鍵字、或改說明時把詞改掉了，這裡會擋下來。
func TestCommonSearchWordsFindTheirItem(t *testing.T) {
	items := allItems()
	for _, c := range []struct{ word, key string }{
		{"半形", "ctrl-punctuation"}, {"半形", "shift-symbol-halfwidth"}, {"全形", "shift-symbol-halfwidth"}, {"符號", "shift-symbol-halfwidth"},
		{"標點", "ctrl-punctuation"}, {"逗號", "ctrl-punctuation"},
		{"簡體", "output-simplified"}, {"簡中", "output-simplified"}, {"繁簡", "output-simplified"}, {"簡體", "simplified-hotkey"},
		{"Shift", "toggle-key"}, {"英文", "toggle-key"}, {"切換", "toggle-key"}, {"中文", "toggle-key"}, {"熱鍵", "toggle-key"},
		{"大寫", "caps-lock-english"}, {"ｃａｐｓ", "caps-lock-english"},
		{"聲調", "toneless"}, {"拼音", "toneless"}, {"拼音", "keyboard-layout"},
		{"倚天", "keyboard-layout"}, {"許氏", "keyboard-layout"}, {"大千", "keyboard-layout"},
		{"捲舌", "fuzzy-z-zh"}, {"ㄓㄗ", "fuzzy-z-zh"}, {"鼻音", "fuzzy-en-eng"},
		{"空白", "space-selects"}, {"空格", "space-selects"}, {"候選", "space-selects"},
		{"表情", "emoji-shortcut"}, {"記憶", "personal-learning"}, {"上下文", "document-context"},
		{"空格", "cjk-latin-space"}, {"空白", "cjk-latin-space"}, {"數字", "cjk-latin-space"}, {"中英文", "cjk-latin-space"}, {"英文", "cjk-latin-space"}, {"盤古", "cjk-latin-space"}, // #365
	} {
		found := false
		for _, it := range items {
			if it.Key != c.key {
				continue
			}
			for _, text := range append(searchableTexts(it), it.Keywords...) {
				found = found || strings.Contains(searchFold(text), searchFold(c.word))
			}
		}
		if !found {
			t.Errorf("搜尋「%s」要找得到 %s", c.word, c.key)
		}
	}
}

func TestSearchFold(t *testing.T) {
	for in, want := range map[string]string{"Ｓｈｉｆｔ＋空白": "shift+空白", "Caps Lock": "caps lock", "半　形": "半 形", "ㄓㄗ": "ㄓㄗ"} {
		if got := searchFold(in); got != want {
			t.Errorf("searchFold(%q) = %q，應是 %q", in, got, want)
		}
	}
}
