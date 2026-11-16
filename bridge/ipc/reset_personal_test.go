package ipc

import (
	"errors"
	"strings"
	"testing"
)

// 清除學習紀錄（#301）：只有宿主接上 ResetPersonal 才認得，health 據此回報 personal_reset；
// 宿主存檔失敗時把錯誤原樣回給呼叫端（0c-update），不說成功。
func TestResetPersonalOnlyWhenHostSupportsIt(t *testing.T) {
	sessions := map[uint64]bool{}
	old := &Server{Engine: toyEngine(t)}
	if r := old.do(Envelope{Op: OpResetPersonal}, sessions, new(bool)); r.OK || !strings.Contains(r.Error, "不認得") {
		t.Fatalf("沒有接上時當成不認得的操作：%+v", r)
	}
	if old.health()["personal_reset"] != "" {
		t.Fatal("沒有接上時 health 不回報 personal_reset")
	}
	calls := 0
	var fail error
	srv := &Server{Engine: toyEngine(t), ResetPersonal: func() error { calls++; return fail }}
	if srv.health()["personal_reset"] != "true" {
		t.Fatal("health 要回報 personal_reset")
	}
	if r := srv.do(Envelope{Op: OpResetPersonal}, sessions, new(bool)); !r.OK || calls != 1 {
		t.Fatalf("%+v calls=%d", r, calls)
	}
	fail = errors.New("磁碟已滿")
	if r := srv.do(Envelope{Op: OpResetPersonal}, sessions, new(bool)); r.OK || r.Error != "磁碟已滿" {
		t.Fatalf("存檔失敗要回報：%+v", r)
	}
}
