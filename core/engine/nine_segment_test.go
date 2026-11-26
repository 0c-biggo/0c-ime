package engine

import "testing"

// 注音九宮格「自動切字」是宿主的設定，之後開的每個 session 都照設定；預設關閉。
func TestOpenSessionCarriesNineSegment(t *testing.T) {
	e := testEngine()
	if e.Session(e.OpenSession()).NineSegment {
		t.Fatal("預設關閉")
	}
	e.NineSegment = true
	if !e.Session(e.OpenSession()).NineSegment {
		t.Fatal("session 沒有帶到自動切字的設定")
	}
}
