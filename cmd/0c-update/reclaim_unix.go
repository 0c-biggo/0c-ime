//go:build darwin || linux

// 只有 macOS 用到（#198，root_darwin.go）；Linux 也編譯，讓這段以 root 執行的程式在 Linux 的 CI 一樣有測試。

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// reclaimFchown 是實際改擁有者的呼叫；測試換成記錄（一般帳號不能把檔案改給別人）。
var reclaimFchown = unix.Fchown

// reclaimOwnership 把 home 底下 rel 這棵樹（#198：靈犀注音自己的路徑）中擁有者是 from 的項目改成 to，
// 回傳改了幾項。以 root 執行、樹又在使用者可以寫的家目錄裡，所以：
//   - 從 home 開始逐層 openat(O_NOFOLLOW)，任何一層（含 rel 的上層）是符號連結就不進去，
//     使用者放的連結不會把 root 帶到家目錄以外。
//   - 先開檔、對開著的檔案 fstat／fchown，檢查與修改的是同一個 inode，中途被換掉也只會改到那個開著的檔案。
//   - 一般檔案只改只有一個名稱的（nlink 1）：使用者可以把系統檔案硬連結進來，那種檔案擁有者也是 root。
//   - root 擁有的 socket 是 root 引擎留下的，引擎已結束，直接刪掉，下次由使用者的引擎重建。
//   - 符號連結、FIFO 與裝置檔一律不動。群組不改（BSD 新檔沿用上層目錄的群組，本來就是使用者的）。
func reclaimOwnership(home, rel string, from, to int) (int, error) {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	dirfd, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}
	for _, p := range parts[:len(parts)-1] {
		next, err := unix.Openat(dirfd, p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(dirfd)
		if err != nil {
			if skippable(err) {
				return 0, nil
			}
			return 0, err
		}
		dirfd = next
	}
	defer unix.Close(dirfd)
	n := 0
	err = reclaimEntry(dirfd, parts[len(parts)-1], from, to, &n)
	return n, err
}

// skippable：不存在、是符號連結（O_NOFOLLOW 回 ELOOP；macOS 對目錄另回 ENOTDIR）的路徑不是我們的，跳過。
func skippable(err error) bool {
	return errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR)
}

func reclaimEntry(dirfd int, name string, from, to int, n *int) error {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if skippable(err) {
			return nil
		}
		return err
	}
	kind := st.Mode & unix.S_IFMT
	switch kind {
	case unix.S_IFSOCK:
		if int(st.Uid) == from {
			if err := unix.Unlinkat(dirfd, name, 0); err == nil {
				*n++
			}
		}
		return nil
	case unix.S_IFREG, unix.S_IFDIR:
	default:
		return nil
	}
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if kind == unix.S_IFDIR {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(dirfd, name, flags, 0)
	if err != nil {
		if skippable(err) {
			return nil
		}
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var fst unix.Stat_t
	if err := unix.Fstat(fd, &fst); err != nil {
		return err
	}
	if fst.Mode&unix.S_IFMT != kind {
		return nil // 檢查之後被換成別的東西
	}
	if int(fst.Uid) == from && (kind == unix.S_IFDIR || uint64(fst.Nlink) == 1) {
		if err := reclaimFchown(fd, to, -1); err != nil {
			return err
		}
		*n++
	}
	if kind != unix.S_IFDIR {
		return nil
	}
	entries, err := f.ReadDir(-1)
	if err != nil {
		return err
	}
	var first error
	for _, e := range entries {
		if err := reclaimEntry(fd, e.Name(), from, to, n); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// foreignOwned 以目前帳號的身分（不需權限）找 home 底下 rels 中擁有者不是 uid 的項目，回傳個數與前幾個路徑。
// 不跟隨符號連結。
func foreignOwned(home string, rels []string, uid int, sample int) (int, []string) {
	count := 0
	var paths []string
	for _, rel := range rels {
		root := filepath.Join(home, rel)
		if !noLinkBetween(home, rel) {
			continue
		}
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			// 不存在、或讀不到內容的目錄（例如 root 的 0700）：目錄本身在第一次呼叫時已經算過，不往下走
			if err != nil || d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			info, e := d.Info()
			if e != nil {
				return nil
			}
			if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != uid {
				count++
				if len(paths) < sample {
					paths = append(paths, path)
				}
			}
			return nil
		})
	}
	return count, paths
}

// noLinkBetween：rel 的上層（home 以下）都不是符號連結。
func noLinkBetween(home, rel string) bool {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	p := home
	for _, part := range parts[:len(parts)-1] {
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}
