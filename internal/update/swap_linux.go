//go:build linux

package update

import "golang.org/x/sys/unix"

// swapPaths 是 Linux 上對應 renamex_np(RENAME_SWAP) 的 renameat2(RENAME_EXCHANGE)。Linux 不安裝 macOS App，
// 這裡讓 macOS 安裝流程的測試在 Linux 也走真正的原子交換；不支援時同樣回傳錯誤、由呼叫端退回。
func swapPaths(a, b string) error {
	return unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_EXCHANGE)
}
