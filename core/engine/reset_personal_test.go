package engine

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/vocab"
)

// 清除學習紀錄（#301）：學到的資料、「不再學」的詞與「靈犀學到的」頁的復原複本全部清掉，存檔就是空白的個人資料；
// 自訂詞彙留著；正在組字的輸入框畫面不動，那段送出時不學。
func TestResetPersonalClearsLearningKeepsVocab(t *testing.T) {
	e := vocabEngine()
	if r := e.Vocab(VocabRequest{Op: VocabAdd, Words: []vocab.Entry{{Text: "世傑", Readings: []string{"ㄕˋ ㄐㄧㄝˊ"}}}}); !r.OK {
		t.Fatalf("%+v", r)
	}
	typing := e.OpenSession()
	typeReading(e, typing, "ㄕˋ ㄐㄧㄝˋ")
	e.Do(typing, proto.Request{Type: proto.Key, Key: proto.KeyEnter})
	st := e.Model.Store
	st.Phrases["世介"] = "ㄕˋ ㄐㄧㄝˋ"
	st.PhraseOrder = append(st.PhraseOrder, "世介")
	st.PhraseSeen["世介"] = 2
	undo := e.Vocab(VocabRequest{Op: VocabForget, Texts: []string{"世介"}})
	if len(st.Bi) == 0 || !st.Tombstones["世介"] || undo.Undo == 0 {
		t.Fatalf("前提：學到東西、有不再學的詞與復原複本 %v %v %+v", st.Total, st.Tombstones, undo)
	}
	composing := e.OpenSession()
	typeReading(e, composing, "ㄕˋ")

	e.ResetPersonal()

	var saved bytes.Buffer
	if err := e.PersonalSnapshot().Save(&saved); err != nil {
		t.Fatal(err)
	}
	var empty bytes.Buffer
	personal.NewStore().Save(&empty)
	if saved.String() != empty.String() {
		t.Fatalf("存檔要是空白的個人資料：\n%s\n%s", saved.String(), empty.String())
	}
	if r := e.Vocab(VocabRequest{Op: VocabLearnedUndo, Undo: undo.Undo}); r.OK || r.Error.Code != vocab.CodeNotFound {
		t.Fatalf("清除後不能用復原把清掉的詞放回來：%+v", r)
	}
	if r := e.Vocab(VocabRequest{Op: VocabStatus}); r.Count != 1 {
		t.Fatalf("自訂詞彙要留著：%+v", r)
	}
	if got := typeReading(e, e.OpenSession(), "ㄕˋ ㄐㄧㄝˊ").Text(); got != "世傑" {
		t.Fatalf("自訂詞照常是首選：%q", got)
	}

	snap, _ := e.Do(composing, proto.Request{Type: proto.Key, Key: proto.KeyEnter})
	if st := e.Model.Store; snap.Commit == "" || st.Total != 0 || len(st.Uni)+len(st.Bi)+len(st.EvidenceByWord) != 0 {
		t.Fatalf("清除前開始的組字照常送出但不學：%q %+v", snap.Commit, st)
	}
	typeReading(e, composing, "ㄕˋ ㄐㄧㄝˋ")
	e.Do(composing, proto.Request{Type: proto.Key, Key: proto.KeyEnter})
	if st := e.Model.Store; st.Total == 0 && len(st.Bi) == 0 {
		t.Fatal("之後打的字照常學")
	}
	var probe map[string]any
	if err := json.Unmarshal(saved.Bytes(), &probe); err != nil || probe["Format"] != float64(personal.StoreFormat) {
		t.Fatalf("存檔格式：%v %v", err, probe["Format"])
	}
}
