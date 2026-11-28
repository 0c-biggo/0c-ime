package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/settings"
)

// #365：「自動加空格」選 all 時，引擎回報 cjk_digit_space 才算已套用；舊引擎只回報 cjk_latin_space 時是 on，
// 選 all 的人看到待套用。running 由 health 的兩個回報合成；只回報數字、沒開中英文的不算 all。
func TestCJKSpaceChoiceContractReportsPending(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"cjk_latin_space":true,"cjk_digit_space":true}`)
	for _, tc := range []struct {
		latin, digit, want string
		pending            bool
	}{{"true", "true", "all", false}, {"true", "", "on", true}, {"true", "false", "on", true}, {"false", "", "off", true}, {"false", "true", "off", true}} {
		info := map[string]string{"cjk_latin_space": tc.latin, "cjk_digit_space": tc.digit}
		if v := runningValues(info)["cjk-latin-space"]; v != tc.want {
			t.Fatalf("%+v：running %q", tc, v)
		}
		doc := buildSettingsDoc(base, modelStatusDial(info, true))
		it := item(t, doc, "cjk-latin-space")
		if it.Value != "all" || it.Default != "off" || it.Locked || it.Pending != tc.pending || it.Running == nil || *it.Running != tc.want {
			t.Fatalf("%+v：%+v", tc, it)
		}
	}
	// 引擎沒回報中英文那一項（更舊的引擎）時不猜。
	if v, ok := runningValues(map[string]string{"cjk_digit_space": "true"})["cjk-latin-space"]; ok {
		t.Fatalf("沒回報時不猜：%q", v)
	}
	if _, ok := runningValues(map[string]string{"cjk_latin_space": "true", "cjk_digit_space": "true"})["cjk-digit-space"]; ok {
		t.Fatal("cjk-digit-space 不是設定的鍵")
	}
	// 設定檔選 on：新引擎回報 false 的數字那項，不待套用。
	writeSettings(t, base, `{"cjk_latin_space":true}`)
	doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"cjk_latin_space": "true", "cjk_digit_space": "false"}, true))
	if it := item(t, doc, "cjk-latin-space"); it.Value != "on" || it.Pending {
		t.Fatalf("on：%+v", it)
	}
	for _, it := range doc.Items {
		if it.Key == "cjk-digit-space" {
			t.Fatalf("設定程式不該有 cjk-digit-space 這一項：%+v", it)
		}
	}
	if o, err := parseSetArgs([]string{"cjk-latin-space", "all", "-apply", "-json"}); err != nil || len(o.pairs) != 1 || !o.apply {
		t.Fatalf("%+v %v", o, err)
	}
}

// 0c-update settings 的列表與 set 的用法：一個鍵、三個值。
func TestCJKSpaceChoiceInListAndUsage(t *testing.T) {
	if !strings.Contains(setUsage, "set cjk-latin-space off|on|all") || strings.Contains(setUsage, "cjk-digit-space") {
		t.Fatalf("用法：%s", setUsage)
	}
	for _, value := range []string{"off", "on", "all"} {
		s := settings.Default()
		if err := s.Set("cjk-latin-space", value); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		listSettings(&out, settings.Report{Settings: s}, "settings.json")
		if !strings.Contains(out.String(), "cjk-latin-space="+value+"（自動加空格：") || strings.Contains(out.String(), "cjk-digit-space") {
			t.Fatalf("%s 的列表：%s", value, out.String())
		}
	}
}
