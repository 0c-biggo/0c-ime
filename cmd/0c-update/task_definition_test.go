package main

import (
	"strings"
	"testing"
)

// 排程定義加上自己的雜湊；工作排程器讀回（正規化、換行與屬性順序可能不同）後仍取得到同一個雜湊。
func TestTaskDefinitionStampRoundTrip(t *testing.T) {
	def := "<Task><RegistrationInfo><Description>靈犀注音自動更新</Description></RegistrationInfo><Actions><Exec><Command>a</Command></Exec></Actions></Task>"
	stamped, stamp := stampTaskDefinition(def)
	if len(stamp) != 16 || !strings.Contains(stamped, "<Documentation>0c-definition:"+stamp+"</Documentation>") {
		t.Fatalf("標記不正確：%s", stamped)
	}
	registered := "<?xml version=\"1.0\" encoding=\"UTF-16\"?>\r\n<Task version=\"1.2\">\r\n  <RegistrationInfo>\r\n    <Description>靈犀注音自動更新</Description>\r\n" +
		"    <Documentation>0c-definition:" + stamp + "</Documentation>\r\n    <URI>\\0c-update-s-1</URI>\r\n  </RegistrationInfo>\r\n</Task>"
	if got := taskDefinitionStamp(registered); got != stamp {
		t.Fatalf("讀回的雜湊 %q，應為 %q", got, stamp)
	}
	if _, other := stampTaskDefinition(strings.Replace(def, "<Command>a", "<Command>b", 1)); other == stamp {
		t.Fatal("動作不同，雜湊應不同")
	}
	if got := taskDefinitionStamp("<Task><RegistrationInfo><Description>舊版</Description></RegistrationInfo></Task>"); got != "" {
		t.Fatalf("舊版排程沒有標記：%q", got)
	}
}

// 排程寫不寫：定義相同不寫；提權時順便把只能讀的舊排程開放給本人；一般權限只在本人能改時才寫，不去碰提權建立、只能讀的排程。
func TestPlanTask(t *testing.T) {
	const want = "aaaa"
	for _, c := range []struct {
		name               string
		exists             bool
		stamp              string
		writable, elevated bool
		action             taskAction
	}{
		{"不存在，一般權限建立", false, "", false, false, taskWrite},
		{"不存在，提權建立", false, "", false, true, taskWrite},
		{"相同而且已開放：提權也不寫", true, want, true, true, taskKeep},
		{"相同但只能讀：提權時開放", true, want, false, true, taskWrite},
		{"0.2.11 的 conhost 定義：提權重寫", true, "", false, true, taskWrite},
		{"相同：一般權限不寫", true, want, false, false, taskKeep},
		{"相同而且能寫：一般權限不寫", true, want, true, false, taskKeep},
		{"已開放的舊定義：一般權限直接換", true, "", true, false, taskWrite},
		{"不同而且只能讀：一般權限不嘗試", true, "bbbb", false, false, taskReadOnly},
		{"0.2.11 提權建立：一般權限不嘗試", true, "", false, false, taskReadOnly},
	} {
		if got := planTask(c.exists, c.stamp, want, c.writable, c.elevated); got != c.action {
			t.Errorf("%s：%v，應為 %v", c.name, got, c.action)
		}
	}
}
