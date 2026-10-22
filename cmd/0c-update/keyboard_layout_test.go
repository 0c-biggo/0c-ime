package main

import "testing"

// #83：鍵盤排列經 settings --json 給設定程式；選倚天 41 鍵時，中英混打與 Emoji 選單標為停用並附原因，
// 值仍是設定檔中的值，與引擎載入的值比較待套用狀態。
func TestKeyboardLayoutContract(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"keyboard_layout":"eten","mixed_input":true}`)
	if v, ok := runningValues(map[string]string{"keyboard_layout": "standard"})["keyboard-layout"]; !ok || v != "standard" {
		t.Fatalf("health 的 keyboard_layout：%q %v", v, ok)
	}
	doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"keyboard_layout": "standard", "mixed_input": "true", "emoji_shortcut": "equals"}, true))
	layout := item(t, doc, "keyboard-layout")
	if layout.Value != "eten" || !layout.Pending || layout.Disabled || !doc.PendingRestart {
		t.Fatalf("排列已儲存、引擎尚未套用：%+v", layout)
	}
	for _, key := range []string{"mixed-input", "emoji-shortcut", "emoji-equals-first"} {
		it := item(t, doc, key)
		if !it.Disabled || it.DisabledReason != "倚天 41 鍵時關閉" || it.Pending {
			t.Fatalf("%s 應停用、不因停用而顯示待套用：%+v", key, it)
		}
	}
	if it := item(t, doc, "mixed-input"); it.Value != "on" {
		t.Fatalf("停用時保留使用者的值：%+v", it)
	}
	if it := item(t, doc, "space-selects"); it.Disabled {
		t.Fatalf("其他設定不受影響：%+v", it)
	}
	if o, err := parseSetArgs([]string{"keyboard-layout", "eten", "-apply", "-json"}); err != nil || len(o.pairs) != 1 || !o.apply {
		t.Fatalf("%+v %v", o, err)
	}
}

// #183：許氏只停用中英混打，Emoji 選單照舊。
func TestKeyboardLayoutContractHsu(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"keyboard_layout":"hsu","mixed_input":true}`)
	doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"keyboard_layout": "hsu", "mixed_input": "true", "emoji_shortcut": "equals"}, true))
	if it := item(t, doc, "keyboard-layout"); it.Value != "hsu" || it.Pending {
		t.Fatalf("許氏已套用：%+v", it)
	}
	if it := item(t, doc, "mixed-input"); !it.Disabled || it.DisabledReason != "許氏排列時關閉" || it.Value != "on" {
		t.Fatalf("混打停用、值保留：%+v", it)
	}
	for _, key := range []string{"emoji-shortcut", "emoji-equals-first"} {
		if it := item(t, doc, key); it.Disabled {
			t.Fatalf("%s 在許氏照舊可用：%+v", key, it)
		}
	}
}
