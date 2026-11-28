package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// 外殼複製整個 session 試查候選、查完放回去（APP 的 frontStep）：試查時把沒有字的注音原樣留下，不能寫進放回去的那份
// 共用的空表（字面位置記在組字範圍外，下一次補全查詢越界）。
func TestCopiedSessionKeepsItsLiterals(t *testing.T) {
	s := newSession()
	s.Toneless, s.Abbreviation, s.MixedWithToneless, s.MixedInput = true, true, true, true
	typeSymbols(s, "ㄨㄛㄍ")       // ㄍ 沒有字：開候選時原樣留下
	s.literals = map[int]bool{} // 刪掉組字裡的字面文字之後是空的表（shiftLiterals 另開的）
	saved := *s
	s.Handle(proto.Request{Type: proto.Candidates})
	*s = saved
	for at := range s.literals {
		if at >= len(s.syls) {
			t.Fatalf("字面位置 %d 在組字範圍外（%d 個位置）", at, len(s.syls))
		}
	}
	s.Completion()
}
