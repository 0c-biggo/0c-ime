//go:build !windows && !darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fakeProc 在暫存目錄建立一個假的 /proc：pid → (comm, 狀態, 父行程)。
func fakeProc(t *testing.T, procs map[int][3]any) {
	t.Helper()
	root := t.TempDir()
	for pid, v := range procs {
		dir := filepath.Join(root, fmt.Sprint(pid))
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		comm, state, ppid := v[0].(string), v[1].(string), v[2].(int)
		os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644)
		os.WriteFile(filepath.Join(dir, "stat"), []byte(fmt.Sprintf("%d (%s) %s %d 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0", pid, comm, state, ppid)), 0o644)
	}
	old, oldSettle := procRoot, settle
	procRoot, settle = root, func() {}
	t.Cleanup(func() { procRoot, settle = old, oldSettle })
}

// setProc 改寫（或新增）假 /proc 裡的一個行程；start 是啟動時間。
func setProc(t *testing.T, pid int, comm, state string, ppid int, start int) {
	t.Helper()
	dir := filepath.Join(procRoot, fmt.Sprint(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "stat"), []byte(fmt.Sprintf("%d (%s) %s %d 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 %d 0", pid, comm, state, ppid, start)), 0o644)
}

// 外殼用 posix_spawn 啟動 0c-update status --json：子行程 exec 之前 comm 還是 fcitx5，
// 不能被數成第二個 Fcitx5（否則更新偶發回報「有多個 Fcitx5 工作階段」）。
func TestForkChildOfFcitxIsNotASecondDesktop(t *testing.T) {
	fakeProc(t, map[int][3]any{
		49:  {"fcitx5", "S", 4},
		217: {"fcitx5", "R", 49}, // fork 之後、exec 之前的子行程
		60:  {"python3", "S", 4},
	})
	got, err := inputProcesses("fcitx5")
	if err != nil || !slices.Equal(got, []int{49}) {
		t.Fatalf("只應有一個 Fcitx5：%v %v", got, err)
	}
}

// 真的兩個桌面（沒有父子關係）仍然要擋下，不能因為上面的修正而漏掉。
func TestTwoUnrelatedFcitxProcessesAreStillTwo(t *testing.T) {
	fakeProc(t, map[int][3]any{
		49:  {"fcitx5", "S", 4},
		300: {"fcitx5", "S", 1},
		301: {"fcitx5", "R", 300}, // 第二個桌面的 fork 子行程也不算
	})
	got, err := inputProcesses("fcitx5")
	slices.Sort(got)
	if err != nil || !slices.Equal(got, []int{49, 300}) {
		t.Fatalf("應是兩個不相關的 Fcitx5：%v %v", got, err)
	}
}

// 已退出的行程（殭屍）不算，沿用原行為。
func TestZombieFcitxIsIgnored(t *testing.T) {
	fakeProc(t, map[int][3]any{
		49:  {"fcitx5", "S", 4},
		500: {"fcitx5", "Z", 1},
	})
	got, _ := inputProcesses("fcitx5")
	if !slices.Equal(got, []int{49}) {
		t.Fatalf("殭屍不應計入：%v", got)
	}
}

// 引擎行程的計數同一個道理：0c-server 的 fork 子行程在 exec 前也叫 0c-server。
func TestForkChildOfServerIsNotASecondServer(t *testing.T) {
	fakeProc(t, map[int][3]any{
		71: {"0c-server", "S", 1},
		90: {"0c-server", "R", 71},
	})
	got, _ := inputProcesses("0c-server", "zuin-server")
	if !slices.Equal(got, []int{71}) {
		t.Fatalf("只應有一個引擎：%v", got)
	}
}

// 外殼用兩次 fork 啟動 0c-server：中間層一 fork 完就退出，孫行程在 exec 之前仍叫 fcitx5，而且已被 init
// 接手（父行程不在名單內）。它很快 exec 成 0c-server；重掃確認後不能算成第二個 Fcitx5。
func TestDoubleForkedGrandchildIsNotASecondDesktop(t *testing.T) {
	fakeProc(t, map[int][3]any{
		49:  {"fcitx5", "S", 4},
		900: {"fcitx5", "R", 1}, // 兩次 fork 的孫行程，exec 之前
	})
	settle = func() { setProc(t, 900, "0c-server", "S", 1, 0) }
	got, err := inputProcesses("fcitx5")
	if err != nil || !slices.Equal(got, []int{49}) {
		t.Fatalf("只應有一個 Fcitx5：%v %v", got, err)
	}
}

// 重掃之間行程消失、PID 被另一個同名行程重用（啟動時間不同），也不能當成一直都在。
func TestReusedPidIsNotCountedAsPersistent(t *testing.T) {
	fakeProc(t, map[int][3]any{
		49:  {"fcitx5", "S", 4},
		900: {"fcitx5", "R", 1},
	})
	round := 0
	settle = func() { round++; setProc(t, 900, "fcitx5", "R", 1, 1000+round) }
	got, _ := inputProcesses("fcitx5")
	if !slices.Equal(got, []int{49}) {
		t.Fatalf("PID 重用不算持續存在：%v", got)
	}
}

// comm 含「) 」也要從最後一個起算，正確讀到狀態與父行程。
func TestStatWithParenthesesInComm(t *testing.T) {
	fakeProc(t, map[int][3]any{49: {"fcitx5", "S", 4}})
	setProc(t, 77, "a) b", "S", 49, 0)
	os.WriteFile(filepath.Join(procRoot, "77", "comm"), []byte("fcitx5\n"), 0o644)
	got, _ := inputProcesses("fcitx5")
	if !slices.Equal(got, []int{49}) {
		t.Fatalf("父行程應讀成 49：%v", got)
	}
}
