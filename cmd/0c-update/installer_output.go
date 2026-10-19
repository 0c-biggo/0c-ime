package main

import (
	"io"
	"os"
	"unicode/utf8"
)

// utf16OutputEnv 由 Windows 安裝程式（platforms/windows/installer/setup.nsi）設定。Unicode 版 NSIS 的
// nsExec::ExecToStack 只在輸出開頭看得出 UTF-16LE 時照原樣讀入，其餘一律以系統碼頁（繁中 Windows 是
// CP950）解讀，0c-update 的 UTF-8 中文訊息就在錯誤對話框裡變成亂碼（#138）。
const utf16OutputEnv = "ZC_OUTPUT_UTF16"

// installerOutput：安裝程式要求時，把 stdout 與 stderr 接到同一條管線（保持兩者的先後順序），
// 轉成帶 BOM 的 UTF-16LE 寫到原本的 stdout。回傳的函式送出剩下的輸出，必須在結束程式前呼叫。
// 其他呼叫者（設定程式、選單、測試）照舊讀 UTF-8；環境變數讀過就清掉，子程序不會跟著改變輸出。
func installerOutput() (flush func()) {
	if os.Getenv(utf16OutputEnv) != "1" {
		return func() {}
	}
	os.Unsetenv(utf16OutputEnv)
	r, w, err := os.Pipe()
	if err != nil {
		return func() {}
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	done := make(chan struct{})
	go func() {
		defer close(done)
		copyUTF16(stdout, r)
	}()
	return func() {
		os.Stdout, os.Stderr = stdout, stderr
		w.Close()
		<-done
		r.Close()
	}
}

// copyUTF16 把 src 的 UTF-8 轉成 UTF-16LE 寫到 dst，直到 src 結束。
func copyUTF16(dst io.Writer, src io.Reader) {
	var e utf16Encoder
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			dst.Write(e.encode(buf[:n], false))
		}
		if err != nil {
			dst.Write(e.encode(nil, true))
			return
		}
	}
}

// utf16Encoder 把分段到達的 UTF-8 轉成 UTF-16LE：第一段前加 BOM（nsExec 靠它判斷編碼，
// 第一次讀到的資料就必須帶著），換行改成 CRLF（對話框與詳細資訊都是 Windows 控制項），
// 跨段的多位元組字元留到下一段再轉，無效位元組換成 U+FFFD。
type utf16Encoder struct {
	pending []byte
	started bool
	lastCR  bool
}

func (e *utf16Encoder) encode(p []byte, final bool) []byte {
	data := append(e.pending, p...)
	e.pending = nil
	out := make([]byte, 2, 2*len(data)+6)
	out[0], out[1] = 0xff, 0xfe
	put := func(r rune) {
		if r >= 0x10000 {
			r -= 0x10000
			hi, lo := 0xd800+(r>>10), 0xdc00+(r&0x3ff)
			out = append(out, byte(hi), byte(hi>>8), byte(lo), byte(lo>>8))
			return
		}
		out = append(out, byte(r), byte(r>>8))
	}
	for len(data) > 0 {
		if !final && !utf8.FullRune(data) {
			e.pending = append([]byte(nil), data...)
			break
		}
		r, size := utf8.DecodeRune(data)
		data = data[size:]
		if r == '\n' && !e.lastCR {
			put('\r')
		}
		e.lastCR = r == '\r'
		put(r)
	}
	if len(out) == 2 {
		return nil
	}
	if e.started {
		return out[2:]
	}
	// BOM 與第一個字一起送出，nsExec 第一次讀到的資料才判斷得出 UTF-16LE。
	e.started = true
	return out
}
