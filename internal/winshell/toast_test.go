package winshell

import (
	"encoding/xml"
	"testing"
)

type parsedToast struct {
	Scenario string `xml:"scenario,attr"`
	Binding  struct {
		Template string   `xml:"template,attr"`
		Text     []string `xml:"text"`
	} `xml:"visual>binding"`
	Actions []struct {
		Content   string `xml:"content,attr"`
		Type      string `xml:"activationType,attr"`
		Arguments string `xml:"arguments,attr"`
	} `xml:"actions>action"`
}

func parse(t *testing.T, s string) parsedToast {
	t.Helper()
	var p parsedToast
	if err := xml.Unmarshal([]byte(s), &p); err != nil {
		t.Fatalf("%v\n%s", err, s)
	}
	return p
}

// 標題與內文原樣送出：引號、角括號、& 都要跳脫，不能讓內容改變 XML 結構。
func TestToastXMLEscapesText(t *testing.T) {
	title, body := `靈犀注音 <0.2.12> & "測試"`, "已更新\n請重新開啟 'App'"
	p := parse(t, ToastXML(Toast{Title: title, Body: body}))
	if p.Binding.Template != "ToastGeneric" || len(p.Binding.Text) != 2 || p.Binding.Text[0] != title || p.Binding.Text[1] != body {
		t.Fatalf("%+v", p)
	}
	if p.Scenario != "" || len(p.Actions) != 0 {
		t.Fatalf("沒有按鈕的通知不加 reminder：%+v", p)
	}
}

// 有按鈕時：reminder 情境、「立即安裝」「稍後 1 小時」兩個 protocol 按鈕，網址裡的 & 正確跳脫。
func TestToastXMLAddsProtocolButtons(t *testing.T) {
	action := "zc-update:finish?sha256=ab&version=0.2.12"
	snooze := "zc-update:snooze?sha256=ab&version=0.2.12"
	p := parse(t, ToastXML(Toast{Title: "t", Body: "b", Action: action, Snooze: snooze}))
	if p.Scenario != "reminder" || len(p.Actions) != 2 {
		t.Fatalf("%+v", p)
	}
	for i, want := range []struct{ content, args string }{{"立即安裝", action}, {"稍後 1 小時", snooze}} {
		a := p.Actions[i]
		if a.Content != want.content || a.Type != "protocol" || a.Arguments != want.args {
			t.Fatalf("按鈕 %d：%+v", i, a)
		}
	}
}
