// Package binfmt 是執行期資料包的底層格式：魔術字、版本、依序排列的區段、CRC32 校驗。
// 全部為小端序、不含指標，所以同一份位元組可以直接讀入，日後也可以 mmap。
package binfmt

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
)

// Writer 依序寫入區段。
type Writer struct{ buf []byte }

func NewWriter(magic string, version uint32) *Writer {
	w := &Writer{}
	w.buf = append(w.buf, magic...)
	w.U32(version)
	return w
}

func (w *Writer) U32(v uint32) { w.buf = binary.LittleEndian.AppendUint32(w.buf, v) }

func (w *Writer) U16s(xs []uint16) {
	w.U32(uint32(len(xs)))
	for _, x := range xs {
		w.buf = binary.LittleEndian.AppendUint16(w.buf, x)
	}
}

func (w *Writer) U32s(xs []uint32) {
	w.U32(uint32(len(xs)))
	for _, x := range xs {
		w.buf = binary.LittleEndian.AppendUint32(w.buf, x)
	}
}

func (w *Writer) U64s(xs []uint64) {
	w.U32(uint32(len(xs)))
	for _, x := range xs {
		w.buf = binary.LittleEndian.AppendUint64(w.buf, x)
	}
}

func (w *Writer) F32s(xs []float32) {
	w.U32(uint32(len(xs)))
	for _, x := range xs {
		w.buf = binary.LittleEndian.AppendUint32(w.buf, math.Float32bits(x))
	}
}

func (w *Writer) Bytes8(xs []uint8) {
	w.U32(uint32(len(xs)))
	w.buf = append(w.buf, xs...)
}

func (w *Writer) String(s string) {
	w.U32(uint32(len(s)))
	w.buf = append(w.buf, s...)
}

// Finish 附上整份內容的 CRC32 並回傳。
func (w *Writer) Finish() []byte {
	return binary.LittleEndian.AppendUint32(w.buf, crc32.ChecksumIEEE(w.buf))
}

// Reader 依序讀出區段。任何錯誤都記在 Err，之後的讀取回傳零值；呼叫端最後檢查一次即可。
type Reader struct {
	b   []byte
	Err error
}

// NewReader 驗證魔術字、版本與 CRC32。
func NewReader(b []byte, magic string, version uint32) *Reader {
	r := &Reader{}
	if len(b) < len(magic)+8 || string(b[:len(magic)]) != magic {
		r.Err = errors.New("不是預期的資料檔（魔術字不符）")
		return r
	}
	body, sum := b[:len(b)-4], binary.LittleEndian.Uint32(b[len(b)-4:])
	if crc32.ChecksumIEEE(body) != sum {
		r.Err = errors.New("資料檔損毀（CRC32 不符）")
		return r
	}
	r.b = body[len(magic):]
	if v := r.U32(); v != version {
		r.Err = fmt.Errorf("資料檔版本 %d，程式需要 %d", v, version)
	}
	return r
}

func (r *Reader) take(n int) []byte {
	if r.Err != nil {
		return nil
	}
	if n < 0 || n > len(r.b) {
		r.Err = errors.New("資料檔截斷")
		return nil
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}

func (r *Reader) U32() uint32 {
	if b := r.take(4); b != nil {
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}

func (r *Reader) count(size int) int {
	n := int(r.U32())
	if r.Err == nil && n*size > len(r.b) {
		r.Err = errors.New("資料檔截斷")
		return 0
	}
	return n
}

func (r *Reader) U16s() []uint16 {
	n := r.count(2)
	b := r.take(2 * n)
	out := make([]uint16, n)
	for i := range out {
		out[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return out
}

func (r *Reader) U32s() []uint32 {
	n := r.count(4)
	b := r.take(4 * n)
	out := make([]uint32, n)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(b[4*i:])
	}
	return out
}

func (r *Reader) U64s() []uint64 {
	n := r.count(8)
	b := r.take(8 * n)
	out := make([]uint64, n)
	for i := range out {
		out[i] = binary.LittleEndian.Uint64(b[8*i:])
	}
	return out
}

func (r *Reader) F32s() []float32 {
	n := r.count(4)
	b := r.take(4 * n)
	out := make([]float32, n)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out
}

func (r *Reader) Bytes8() []uint8 {
	n := r.count(1)
	return append([]uint8(nil), r.take(n)...)
}

func (r *Reader) String() string {
	n := r.count(1)
	return string(r.take(n))
}

// Done 確認所有內容都已讀完。
func (r *Reader) Done() error {
	if r.Err == nil && len(r.b) != 0 {
		r.Err = fmt.Errorf("資料檔結尾有 %d 位元組未讀", len(r.b))
	}
	return r.Err
}
