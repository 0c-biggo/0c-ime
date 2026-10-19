package eval

import "testing"

func TestRewriteClassification(t *testing.T) {
	var s RewriteStats
	s.Observe("不", "不值", "不直覺", 2)
	s.Observe("不值", "布直覺", "不直覺", 3)
	if s.Compared != 3 || s.CorrectToWrong != 1 || s.WrongToCorrect != 1 || s.WrongToWrong != 0 {
		t.Fatal(s)
	}
	s.Observe("心", "昕圖", "新圖", 2)
	if s.WrongToWrong != 1 {
		t.Fatal(s)
	}
	before := s
	s.Observe("ㄒㄧㄣ", "昕圖", "新圖", 2)
	if s != before {
		t.Fatal("原樣注音不能當成對齊的漢字")
	}
}
