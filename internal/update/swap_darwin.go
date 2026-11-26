//go:build darwin

package update

import "golang.org/x/sys/unix"

// swapPaths 以 renamex_np(RENAME_SWAP) 原子交換兩個路徑（#354）。兩者都必須存在、在同一個卷宗；
// 不同卷宗（EXDEV）或檔案系統不支援（ENOTSUP、EINVAL）時回傳錯誤、什麼都不動，由呼叫端退回先移走再放入。
func swapPaths(a, b string) error { return unix.RenamexNp(a, b, unix.RENAME_SWAP) }
