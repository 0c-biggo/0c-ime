package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// choseShemma 只記下使用者用 ㄕㄜˊ ㄇㄜ˙ 選過「什麼」的個人讀音並遞增版本，模擬別的欄位提交後的學習；
// 「什麼」的讀音權重本來就是 1，候選的分數不變。
func choseShemma(s *Session) {
	s.model.Store.Readings = map[string]bool{"什麼\t" + zhuyin.Key(zhuyin.ParseReading("ㄕㄜˊ ㄇㄜ˙")): true}
	s.model.Gen++
}

// #101：排除候選的結果改在快取沒命中、或個人資料版本變了才重算。段落的候選與分數都沒變、
// 只有個人資料變了，下一次排序仍要照新的資料排除「甚麼」。
func TestRerankCacheFollowsPersonalDataChanges(t *testing.T) {
	s := newShemeSession(t)
	typeReadingLocal(s, "ㄕㄜˊ ㄇㄜ˙")
	if got := key(s, "<").Text(); got != "甚麼，" {
		t.Fatalf("測試前提：評分器應把首選換成「甚麼」，實得 %q", got)
	}
	choseShemma(s)
	if got := typeSyl(s, "ㄕˋ").Text(); got != "什麼，是" {
		t.Fatalf("個人資料改變後前一段應改回使用者選過的「什麼」，實得 %q", got)
	}
}

// #101：快取只省下重算，結果與每鍵都重新排序完全相同。對照組每次處理按鍵前清空快取。
func TestRerankCacheMatchesUncachedRanking(t *testing.T) {
	steps := []func(*Session) proto.Snapshot{
		func(s *Session) proto.Snapshot { return typeReadingLocal(s, "ㄨㄟˋ ㄕㄜˊ ㄇㄜ˙") },
		func(s *Session) proto.Snapshot { return key(s, "<") },
		func(s *Session) proto.Snapshot { return typeReadingLocal(s, "ㄕㄜˊ ㄇㄜ˙ ㄕˊ ㄏㄡˋ") },
		func(s *Session) proto.Snapshot { return key(s, proto.KeyBackspace) },
		func(s *Session) proto.Snapshot { return key(s, proto.KeyBackspace) },
		func(s *Session) proto.Snapshot { return typeReadingLocal(s, "ㄏㄡˋ") },
		func(s *Session) proto.Snapshot { choseShemma(s); return typeReadingLocal(s, "ㄕˋ") },
		func(s *Session) proto.Snapshot { return key(s, "<") },
		func(s *Session) proto.Snapshot { return typeReadingLocal(s, "ㄕㄜˊ ㄇㄜ˙") },
		func(s *Session) proto.Snapshot { return key(s, proto.KeyBackspace) },
		func(s *Session) proto.Snapshot { return typeReadingLocal(s, "ㄇㄜ˙") },
	}
	cached, plain := newShemeSession(t), newShemeSession(t)
	for i, step := range steps {
		plain.rerankCache = nil
		want := step(plain)
		if got := step(cached); got.Text() != want.Text() || got.Suggestion != want.Suggestion {
			t.Fatalf("第 %d 步：快取 %q（%q），不快取 %q（%q）", i, got.Text(), got.Suggestion, want.Text(), want.Suggestion)
		}
	}
}
