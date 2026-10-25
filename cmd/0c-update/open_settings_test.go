package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/settingsapp"
)

type openerProbe struct {
	opener   settingsOpener
	launched []string // 啟動時收到的路徑與選項
	options  settingsapp.LaunchOptions
	notices  []string
	entries  int
}

func newProbe() *openerProbe {
	p := &openerProbe{}
	p.opener = settingsOpener{
		locate: func() (settingsapp.Location, error) {
			return settingsapp.Location{Path: "/core/0c-settings", Source: settingsapp.SourceCore}, nil
		},
		display: func() bool { return true },
		missing: func(string) []string { return nil },
		launch: func(path string, o settingsapp.LaunchOptions) error {
			p.launched, p.options = append(p.launched, path), o
			return nil
		},
		notify:  func(title, body string) { p.notices = append(p.notices, title+"："+body) },
		entry:   func() { p.entries++ },
		hint:    func(libs []string) string { return "請安裝 libwebkit2gtk-4.1-0。" },
		updater: "/stable/0c-update",
		log:     "/state/settings-last.log",
		wait:    time.Second,
	}
	return p
}

func TestOpenSettingsLaunchesVerifiedProgramDetachedWithPageAndUpdater(t *testing.T) {
	p := newProbe()
	if err := p.opener.open("update"); err != nil {
		t.Fatal(err)
	}
	if len(p.launched) != 1 || p.launched[0] != "/core/0c-settings" {
		t.Fatalf("%v", p.launched)
	}
	if o := p.options; o.Page != "update" || o.Updater != "/stable/0c-update" || o.Log != "/state/settings-last.log" || o.Wait != time.Second {
		t.Fatalf("%+v", o)
	}
	if len(p.notices) != 0 || p.entries != 1 {
		t.Fatalf("成功時不通知，但要順手確認應用程式選單的項目：%v entries=%d", p.notices, p.entries)
	}
}

func TestOpenSettingsExplainsEveryWayItCannotOpenAndNeverLaunches(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(*openerProbe)
		want   []string
	}{
		{"沒有附設定程式", func(p *openerProbe) {
			p.opener.locate = func() (settingsapp.Location, error) { return settingsapp.Location{}, settingsapp.ErrNotFound }
		}, []string{"沒有附設定程式", "0c-update settings", "0c-update set"}},
		{"核對不過", func(p *openerProbe) {
			p.opener.locate = func() (settingsapp.Location, error) {
				return settingsapp.Location{}, errors.New("核心版本目錄的設定程式：雜湊與記錄不符")
			}
		}, []string{"無法通過核對", "雜湊與記錄不符", "重新安裝", "0c-update settings"}},
		{"沒有圖形桌面", func(p *openerProbe) { p.opener.display = func() bool { return false } },
			[]string{"找不到圖形桌面", "WAYLAND_DISPLAY", "0c-update settings"}},
		{"缺 WebKitGTK", func(p *openerProbe) {
			p.opener.missing = func(string) []string { return []string{"libwebkit2gtk-4.1.so.0", "libgtk-3.so.0"} }
		}, []string{"libwebkit2gtk-4.1.so.0", "libgtk-3.so.0", "找不到", "請安裝 libwebkit2gtk-4.1-0", "0c-update set"}},
	} {
		p := newProbe()
		c.mutate(p)
		err := p.opener.open("overview")
		if err == nil {
			t.Errorf("%s：應回報錯誤", c.name)
			continue
		}
		for _, want := range c.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s：訊息缺少「%s」：%v", c.name, want, err)
			}
		}
		if len(p.launched) != 0 {
			t.Errorf("%s：不可啟動", c.name)
		}
		// 從選單或捷徑啟動時看不到 stderr：同一段說明要以通知送出。
		if len(p.notices) != 1 || !strings.Contains(p.notices[0], "靈犀注音設定無法開啟") || !strings.Contains(p.notices[0], err.Error()) {
			t.Errorf("%s：通知 %v", c.name, p.notices)
		}
	}
}

func TestOpenSettingsReportsAProgramThatDiesRightAway(t *testing.T) {
	p := newProbe()
	p.opener.launch = func(string, settingsapp.LaunchOptions) error {
		return &settingsapp.ExitError{Code: 127, Tail: "error while loading shared libraries: libwebkit2gtk-4.1.so.0"}
	}
	err := p.opener.open("overview")
	if err == nil || !strings.Contains(err.Error(), "沒有啟動") || !strings.Contains(err.Error(), "libwebkit2gtk-4.1.so.0") || !strings.Contains(err.Error(), "0c-update settings") {
		t.Fatalf("%v", err)
	}
	if len(p.notices) != 1 {
		t.Fatalf("%v", p.notices)
	}
}

func TestOpenSettingsRejectsExtraArguments(t *testing.T) {
	if err := openSettings([]string{"update", "extra"}); err == nil || !strings.Contains(err.Error(), "overview|styles|mode|candidates|keyboard|output|update|troubleshoot|about") {
		t.Fatalf("%v", err)
	}
}
