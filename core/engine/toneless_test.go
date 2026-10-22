package engine

import "testing"

// #150：使用者開啟「不打聲調也能選字」時，之後開的每個 session 都照設定；預設關閉。
// 開著時中英混打停用，閒置聲調鍵跟沒開混打一樣由輸入法處理（Windows 外殼看 tone_mark_keys）。
func TestOpenSessionCarriesToneless(t *testing.T) {
	e := testEngine()
	if e.Session(e.OpenSession()).Toneless {
		t.Fatal("預設要打聲調")
	}
	e.Toneless = true
	if !e.Session(e.OpenSession()).Toneless {
		t.Fatal("session 沒有帶到聲調選填的設定")
	}
	e.MixedInput = true
	if !e.ToneMarkKeys() {
		t.Fatal("不打聲調時混打停用：聲調鍵由輸入法處理")
	}
	plain := testEngine()
	if e.IdleKeys() != plain.IdleKeys() {
		t.Fatalf("不打聲調時閒置按鍵同沒開混打（#183 的 idle_keys）：%q vs %q", e.IdleKeys(), plain.IdleKeys())
	}
	e.Toneless = false
	if e.IdleKeys() == plain.IdleKeys() {
		t.Fatal("關掉不打聲調後閒置按鍵照混打")
	}
	if e.ToneMarkKeys() {
		t.Fatal("關掉不打聲調後混打照舊")
	}
}
