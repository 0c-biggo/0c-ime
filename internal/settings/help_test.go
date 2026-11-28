package settings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// #362：設定程式每一項的說明只有一句，細節在設定參考文件（官網 /guide/settings/）。

var helpGOOS = []string{"windows", "linux", "darwin"}

// settingsReference 讀設定參考文件：從設定程式的說明拿掉的細節都在這裡，各項的測試用它確認沒有弄丟。
// 原始碼樹裡沒有這份文件時（內容在官網 /guide/settings/），用到它的測試略過。
func settingsReference(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../docs/guide/settings-reference.md")
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("沒有 docs/guide/settings-reference.md")
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// textWidth 以全形字為 1、半形字（ASCII）為 0.5 算一段文字的寬度，回傳兩倍值。
func textWidth(s string) int {
	w := 0
	for _, r := range s {
		if r < 0x80 {
			w++
		} else {
			w += 2
		}
	}
	return w
}

// 上限 40 個全形字：設定程式預設視窗寬度下不超過兩行。
const maxTextWidth = 40 * 2

func checkOneSentence(t *testing.T, what, text string) {
	t.Helper()
	if text == "" || !strings.HasSuffix(text, "。") {
		t.Errorf("%s 要是以句號結尾的一句話：%q", what, text)
	}
	// 「。」是舉例打出的句號，不算句子的結尾。
	if strings.Count(strings.ReplaceAll(text, "「。」", ""), "。") != 1 || strings.Contains(text, "\n") {
		t.Errorf("%s 只能有一句：%q", what, text)
	}
	if w := textWidth(text); w > maxTextWidth {
		t.Errorf("%s 太長（%d.%d 個全形字，上限 %d）：%q", what, w/2, w%2*5, maxTextWidth/2, text)
	}
}

// 每一項的說明：一句話、40 個全形字以內。依平台不同的說明三個平台都看。
func TestDescriptionsStayShort(t *testing.T) {
	for _, it := range Default().Items() {
		checkOneSentence(t, it.Key+" 的說明", it.Description)
	}
	for _, goos := range helpGOOS {
		checkOneSentence(t, goos+" simplified-hotkey", simplifiedHotkeyDescription(goos))
		checkOneSentence(t, goos+" ctrl-punctuation", ctrlPunctuationDescription(goos))
		checkOneSentence(t, goos+" zhuyin-error-hint", zhuyinErrorHintDescription(goos))
	}
	for _, p := range Default().Profiles().Items {
		checkOneSentence(t, "輸入習慣「"+p.Name+"」的說明", p.Description)
	}
}

// 選項的提示：只給這一項真的有的值，一句話、40 個全形字以內；沒有提示的項目不帶 value_hints。
func TestValueHintsNameRealValues(t *testing.T) {
	withHints := 0
	for _, it := range Default().Items() {
		for _, goos := range helpGOOS {
			for value, hint := range valueHints(goos, it.Key) {
				if !strings.Contains(","+strings.Join(it.Values, ",")+",", ","+value+",") {
					t.Errorf("%s 沒有 %q 這個值，提示：%q", it.Key, value, hint)
				}
				checkOneSentence(t, goos+" "+it.Key+"="+value+" 的提示", hint)
			}
		}
		if len(it.ValueHints) > 0 {
			withHints++
		}
		b, err := json.Marshal(it)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"value_hints"`) != (len(it.ValueHints) > 0) {
			t.Errorf("%s：沒有提示時不帶 value_hints：%s", it.Key, b)
		}
	}
	if withHints == 0 {
		t.Fatal("沒有任何一項帶 value_hints")
	}
}

// 鍵盤排列的每個選項各有提示（#362 的起因：各種排列的規則原本全寫在同一段說明裡）；
// 倚天、許氏、漢語拼音、小鶴雙拼要寫出這時沒有中英混打。
func TestKeyboardLayoutHints(t *testing.T) {
	var item Item
	for _, it := range Default().Items() {
		if it.Key == "keyboard-layout" {
			item = it
		}
	}
	if item.Description != "注音鍵盤的排列方式。" {
		t.Fatalf("說明只說這個設定做什麼：%q", item.Description)
	}
	for _, value := range item.Values {
		hint := item.ValueHints[value]
		if hint == "" {
			t.Errorf("%s 沒有提示", value)
		}
		if value != LayoutStandard && !strings.Contains(hint, "沒有") {
			t.Errorf("%s 的提示要寫出這時沒有哪些功能：%q", value, hint)
		}
	}
	for value, want := range map[string]string{LayoutETen: "頓號改按 \\", LayoutHsu: "d f j s", LayoutPinyin: "ni3hao3", LayoutXiaohe: "nihc"} {
		if !strings.Contains(item.ValueHints[value], want) {
			t.Errorf("%s 的提示要有 %q：%q", value, want, item.ValueHints[value])
		}
	}
	if !strings.Contains(item.ValueHints[LayoutETen], "Emoji 選單與中英混打") {
		t.Errorf("倚天 41 鍵時沒有 Emoji 選單與中英混打：%q", item.ValueHints[LayoutETen])
	}
}

// 說明連結：官網設定說明頁加 # 與鍵名（0c/website#27，錨點定了不能改）。網址只用設定程式放行的字元
// （platforms/settings/src/updater.rs 的 browser_url）。
func TestHelpURLAnchorsAreKeys(t *testing.T) {
	allowed := regexp.MustCompile(`^https://0c\.biggo\.com/guide/settings/#[a-z0-9-]+$`)
	for _, it := range Default().Items() {
		if it.HelpURL != "https://0c.biggo.com/guide/settings/#"+it.Key || !allowed.MatchString(it.HelpURL) {
			t.Errorf("%s 的說明連結：%q", it.Key, it.HelpURL)
		}
	}
}

// 設定參考文件：每個設定一節，標題下一行「錨點：`鍵名`」；順序照設定程式的頁面。macOS 沒有的 Caps Lock 那一項也要有。
func TestSettingsReferenceCoversEveryItem(t *testing.T) {
	ref := settingsReference(t)
	anchors := map[string]bool{}
	for _, m := range regexp.MustCompile("(?m)^錨點：(.+)$").FindAllStringSubmatch(ref, -1) {
		for _, key := range regexp.MustCompile("`([a-z0-9-]+)`").FindAllStringSubmatch(m[1], -1) {
			if anchors[key[1]] {
				t.Errorf("錨點 %s 重複", key[1])
			}
			anchors[key[1]] = true
		}
	}
	keys := []string{"caps-lock-english"}
	for _, it := range Default().Items() {
		keys = append(keys, it.Key)
	}
	for _, key := range keys {
		if !anchors[key] {
			t.Errorf("docs/guide/settings-reference.md 沒有 %s 這一節（標題下要有「錨點：`%s`」）", key, key)
		}
	}
	// 每一節的標題就是設定的名稱，使用者從設定程式點過去看到同一個名字。
	for _, it := range Default().Items() {
		if strings.HasPrefix(it.Key, "fuzzy-") {
			continue // 四組合成「模糊音」一節
		}
		if !strings.Contains(ref, "\n### "+it.Label+"\n\n錨點：`"+it.Key+"`") {
			t.Errorf("%s 這一節的標題要是「%s」，下一行是錨點", it.Key, it.Label)
		}
	}
	if !utf8.ValidString(ref) || strings.Contains(ref, "0.2.") {
		t.Error("設定參考文件只寫現況，不寫版本標記")
	}
}

// 頁首的「說明」連結（輸入習慣、更新、我的詞彙，platforms/settings/ui/item-help.js 的 PAGE_ANCHORS）開的三個錨點
// 在設定參考文件裡各有一節，並寫明是哪一頁連過來的；跟設定的鍵名不能撞。
func TestSettingsReferenceHasThePageAnchors(t *testing.T) {
	ref := settingsReference(t)
	keys := map[string]bool{"caps-lock-english": true}
	for _, it := range Default().Items() {
		keys[it.Key] = true
	}
	for anchor, page := range map[string]string{"input-styles": "輸入習慣", "update-mode": "更新", "vocab": "我的詞彙"} {
		if keys[anchor] {
			t.Errorf("頁面的錨點 %s 跟設定的鍵名相同", anchor)
		}
		if !strings.Contains(ref, "\n## "+page+"\n\n錨點：`"+anchor+"`（頁面的錨點，設定程式「"+page+"」頁首的「說明」連結開這裡）\n") {
			t.Errorf("docs/guide/settings-reference.md 的「%s」一節要標錨點 %s，並寫明頁首的連結開這裡", page, anchor)
		}
	}
	// 拼音排列下開了不打聲調：空白把拼音換成字並直接送出（#324）；兩節都要寫到。
	for _, section := range []string{"### 鍵盤排列", "### 不打聲調也能選字"} {
		body := ref[strings.Index(ref, section):]
		body = body[:strings.Index(body[1:], "\n### ")+1]
		if !strings.Contains(body, "把拼音換成字並") || !strings.Contains(body, "送出") || !strings.Contains(body, "↓ 或方向鍵") {
			t.Errorf("「%s」要寫漢語拼音不打聲調時空白把拼音換成字並送出、想先看字按 ↓ 或方向鍵", section)
		}
	}
}
