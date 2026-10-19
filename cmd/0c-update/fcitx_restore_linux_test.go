//go:build !windows && !darwin

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// restoreStub 替換補回輸入法清單會用到的系統操作，記錄呼叫。
type restoreStub struct {
	pids           []int
	pidsErr        error
	shellReady     bool
	has            bool
	hasErr, addErr error
	profileErr     error
	boot           string
	profileAdds    int
	liveQueries    int
	liveAdds       int
}

func stubRestore(t *testing.T, s *restoreStub) string {
	t.Helper()
	oldPids, oldReady, oldProfile, oldHas, oldAdd, oldBoot := restoreFcitxPids, restoreShellReady, restoreProfileAdd, restoreLiveHas, restoreLiveAdd, currentBootID
	t.Cleanup(func() {
		restoreFcitxPids, restoreShellReady, restoreProfileAdd, restoreLiveHas, restoreLiveAdd, currentBootID = oldPids, oldReady, oldProfile, oldHas, oldAdd, oldBoot
	})
	restoreFcitxPids = func() ([]int, error) { return s.pids, s.pidsErr }
	restoreShellReady = func() bool { return s.shellReady }
	restoreProfileAdd = func() error { s.profileAdds++; return s.profileErr }
	restoreLiveHas = func() (bool, error) { s.liveQueries++; return s.has, s.hasErr }
	restoreLiveAdd = func() error {
		s.liveAdds++
		if s.addErr == nil {
			s.has = true
		}
		return s.addErr
	}
	currentBootID = func() string { return s.boot }
	return t.TempDir()
}

func writeRestoreMarker(t *testing.T, base string, todo zuinRestore) string {
	t.Helper()
	path := filepath.Join(base, zuinRestoreMarker)
	os.MkdirAll(filepath.Dir(path), 0o755)
	b, _ := json.Marshal(todo)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readRestoreMarker(t *testing.T, path string) (zuinRestore, bool) {
	t.Helper()
	var todo zuinRestore
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return todo, false
	}
	if err != nil || json.Unmarshal(b, &todo) != nil {
		t.Fatalf("待辦讀不出來：%v %s", err, b)
	}
	return todo, true
}

// 沒有待辦（不是在卡住狀態裝的，或已經做過）時完全不碰 Fcitx5：使用者自己拿掉的靈犀注音不加回。
func TestRestoreInputMethodNeedsTheStuckMarker(t *testing.T) {
	s := &restoreStub{pids: []int{49}, shellReady: true, boot: "b1"}
	base := stubRestore(t, s)
	restoreStuckInputMethod(base, true)
	restoreStuckInputMethod(base, false)
	s.pids = nil
	restoreStuckInputMethod(base, true)
	if s.profileAdds+s.liveQueries+s.liveAdds != 0 {
		t.Fatalf("沒有待辦卻動了輸入法清單：%+v", s)
	}
}

// 外殼載入後以 D-Bus 補回一次，之後清除待辦；使用者再拿掉也不再加回。
func TestRestoreInputMethodAddsOnceAfterTheShellLoads(t *testing.T) {
	s := &restoreStub{pids: []int{49}, boot: "b1"}
	base := stubRestore(t, s)
	path := writeRestoreMarker(t, base, zuinRestore{Schema: 1, Version: "0.1.0-dev.61"})
	// 重新登入前：執行中的還是沒載入外殼的 Fcitx5，加了也會被丟掉。
	restoreStuckInputMethod(base, false)
	if s.liveQueries+s.liveAdds != 0 {
		t.Fatal("外殼沒載入時不能加")
	}
	if _, ok := readRestoreMarker(t, path); !ok {
		t.Fatal("外殼沒載入時要保留待辦")
	}
	s.shellReady = true
	restoreStuckInputMethod(base, false)
	if s.liveAdds != 1 || !s.has {
		t.Fatalf("外殼載入後應以 D-Bus 加回一次：%+v", s)
	}
	if _, ok := readRestoreMarker(t, path); ok {
		t.Fatal("加回後要清除待辦")
	}
	s.has = false // 使用者之後自己拿掉
	restoreStuckInputMethod(base, false)
	restoreStuckInputMethod(base, true)
	if s.liveAdds != 1 || s.liveQueries != 1 {
		t.Fatalf("做過一次後不再干涉：%+v", s)
	}
}

func TestRestoreInputMethodClearsWhenZuinIsAlreadyThere(t *testing.T) {
	s := &restoreStub{pids: []int{49}, shellReady: true, has: true, boot: "b1"}
	base := stubRestore(t, s)
	path := writeRestoreMarker(t, base, zuinRestore{Schema: 1})
	restoreStuckInputMethod(base, false)
	if s.liveAdds != 0 {
		t.Fatal("已有時不再加")
	}
	if _, ok := readRestoreMarker(t, path); ok {
		t.Fatal("確認已有後要清除待辦")
	}
}

// Fcitx5 不在執行、D-Bus 失敗、有多個 Fcitx5 都不報錯中斷，保留待辦下次再試。
func TestRestoreInputMethodKeepsTheMarkerWhenItCannotFinish(t *testing.T) {
	for _, c := range []struct {
		name  string
		login bool
		s     restoreStub
	}{
		{"Fcitx5 沒在執行（不是登入時）", false, restoreStub{}},
		{"行程讀不到", true, restoreStub{pidsErr: os.ErrPermission}},
		{"多個 Fcitx5", false, restoreStub{pids: []int{49, 50}, shellReady: true}},
		{"查詢群組失敗", false, restoreStub{pids: []int{49}, shellReady: true, hasErr: fmt.Errorf("org.freedesktop.DBus.Error.ServiceUnknown")}},
		{"加入失敗或沒保留", false, restoreStub{pids: []int{49}, shellReady: true, addErr: fmt.Errorf("Fcitx5 沒有保留靈犀注音")}},
		{"登入時寫 profile 失敗", true, restoreStub{profileErr: os.ErrPermission}},
	} {
		s := c.s
		s.boot = "b1"
		base := stubRestore(t, &s)
		path := writeRestoreMarker(t, base, zuinRestore{Schema: 1})
		restoreStuckInputMethod(base, c.login)
		todo, ok := readRestoreMarker(t, path)
		if !ok || todo.ProfileBoot != "" {
			t.Errorf("%s：待辦要原樣保留（%v %+v）", c.name, ok, todo)
		}
	}
}

// 登入時、桌面啟動前 Fcitx5 還沒執行：直接寫進 profile，Fcitx5 一啟動就有靈犀注音；待辦留到 D-Bus 確認。
// 同一次開機裡之後群組沒有 zuin，是使用者登入後自己拿掉的，不再加回。
func TestRestoreInputMethodWritesTheProfileAtLoginThenConfirms(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	profile := filepath.Join(cfg, "fcitx5/profile")
	os.MkdirAll(filepath.Dir(profile), 0o755)
	// 沒載入外殼的 Fcitx5 登出時存回的 profile：只剩鍵盤。
	saved := "[Groups/0]\n# Group Name\nName=Default\n# Layout\nDefault Layout=us\n# Default Input Method\nDefaultIM=keyboard-us\n\n" +
		"[Groups/0/Items/0]\n# Name\nName=keyboard-us\n# Layout\nLayout=\n\n[GroupOrder]\n0=Default\n\n"
	os.WriteFile(profile, []byte(saved), 0o644)

	s := &restoreStub{boot: "b1"}
	base := stubRestore(t, s)
	restoreProfileAdd = func() error { s.profileAdds++; return addInputMethod() }
	path := writeRestoreMarker(t, base, zuinRestore{Schema: 1})
	restoreStuckInputMethod(base, true)
	b, _ := os.ReadFile(profile)
	if s.profileAdds != 1 || !zuinItem.Match(b) {
		t.Fatalf("登入時要把 zuin 寫回 profile：\n%s", b)
	}
	todo, ok := readRestoreMarker(t, path)
	if !ok || todo.ProfileBoot != "b1" {
		t.Fatalf("寫了 profile 仍要保留待辦並記下這次開機：%v %+v", ok, todo)
	}
	// Fcitx5 啟動、載入外殼：群組裡有 zuin，確認後清除。
	s.pids, s.shellReady, s.has = []int{49}, true, true
	restoreStuckInputMethod(base, false)
	if _, ok := readRestoreMarker(t, path); ok || s.liveAdds != 0 {
		t.Fatalf("確認已有後清除且不再加：%+v", s)
	}

	// 同一次開機、群組卻沒有 zuin：使用者登入後自己拿掉的，清除待辦、不加回。
	s = &restoreStub{pids: []int{49}, shellReady: true, boot: "b1"}
	base = stubRestore(t, s)
	path = writeRestoreMarker(t, base, zuinRestore{Schema: 1, ProfileBoot: "b1"})
	restoreStuckInputMethod(base, false)
	if _, ok := readRestoreMarker(t, path); ok || s.liveAdds != 0 {
		t.Fatalf("登入後使用者自己拿掉的不加回：%+v", s)
	}
	// 寫過 profile 後重新開機、這次登入時沒寫到（Fcitx5 已在執行）：照常以 D-Bus 補回。
	s = &restoreStub{pids: []int{49}, shellReady: true, boot: "b2"}
	base = stubRestore(t, s)
	path = writeRestoreMarker(t, base, zuinRestore{Schema: 1, ProfileBoot: "b1"})
	restoreStuckInputMethod(base, false)
	if _, ok := readRestoreMarker(t, path); ok || s.liveAdds != 1 {
		t.Fatalf("別次開機要以 D-Bus 補回：%+v", s)
	}
}

// 登入時 Fcitx5 還在執行（使用者管理員與 D-Bus 跨登出存活）：不改 profile（Fcitx5 結束時會覆寫），外殼沒載入也不加。
func TestRestoreInputMethodDoesNotEditTheProfileWhileFcitxRuns(t *testing.T) {
	s := &restoreStub{pids: []int{49}, boot: "b1"}
	base := stubRestore(t, s)
	path := writeRestoreMarker(t, base, zuinRestore{Schema: 1})
	restoreStuckInputMethod(base, true)
	if s.profileAdds != 0 || s.liveQueries+s.liveAdds != 0 {
		t.Fatalf("沒載入外殼的 Fcitx5 還在執行時什麼都不改：%+v", s)
	}
	if _, ok := readRestoreMarker(t, path); !ok {
		t.Fatal("要保留待辦")
	}
}
