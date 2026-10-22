package settingsapp

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// 頁面名稱有三份：這裡（0c-update open-settings 的白名單）、設定程式 Rust 端（--page 與 macOS 切頁通知）、
// 畫面（側欄與舊名稱的導向）。少了一份，那個名稱就會開成總覽（#207 拆頁時三份一起改）。
func TestPageNamesMatchTheSettingsApp(t *testing.T) {
	root := filepath.Join("..", "..", "platforms", "settings")
	quoted := regexp.MustCompile(`"([a-z]+)"|'([a-z]+)'`)
	names := func(src string) []string {
		var out []string
		for _, m := range quoted.FindAllStringSubmatch(src, -1) {
			out = append(out, m[1]+m[2])
		}
		slices.Sort(out)
		return out
	}
	read := func(path string) string {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	want := slices.Sorted(slices.Values(append(append([]string{}, Pages...), hiddenPages...)))

	rust := regexp.MustCompile(`(?s)pub const PAGES: \[&str; \d+\] = \[(.*?)\];`).FindStringSubmatch(read("src/updater.rs"))
	if rust == nil {
		t.Fatal("找不到 updater.rs 的 PAGES")
	}
	if got := names(rust[1]); !slices.Equal(got, want) {
		t.Errorf("updater.rs 的 PAGES = %v，應為 %v", got, want)
	}

	app := read("ui/app.js")
	pages := regexp.MustCompile(`const PAGES = \[(.*?)\];`).FindStringSubmatch(app)
	alias := regexp.MustCompile(`const PAGE_ALIAS = \{(.*?)\};`).FindStringSubmatch(app)
	if pages == nil || alias == nil {
		t.Fatal("找不到 app.js 的 PAGES 或 PAGE_ALIAS")
	}
	keys := regexp.MustCompile(`([a-z]+):`).FindAllStringSubmatch(alias[1], -1)
	got := names(pages[1])
	for _, k := range keys {
		got = append(got, k[1])
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("app.js 的頁面與舊名稱 = %v，應為 %v", got, want)
	}
	// 舊的「輸入設定」導到三頁中的一頁。
	if !regexp.MustCompile(`input: '(mode|candidates|keyboard)'`).MatchString(alias[1]) {
		t.Errorf("input 要導到拆開後的頁面：%s", alias[1])
	}
}
