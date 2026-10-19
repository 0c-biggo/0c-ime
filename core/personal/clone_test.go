package personal

import (
	"reflect"
	"testing"
)

// Clone 必須複製每個 map 與 slice；Store 新增這類欄位卻沒在 Clone 複製時，這裡會失敗。
func TestStoreCloneSharesNoMapsOrSlices(t *testing.T) {
	s := NewStore()
	s.Picked["ㄇㄚˊ"] = []string{"麻"}
	s.PhraseOrder = []string{"麻婆"}
	s.EmojiRecent = []string{"😀"}
	s.Pronouns["他"] = PronounHabit{Cues: map[string]PronounCount{"老師": {}}}
	c := s.Clone()
	sv, cv := reflect.ValueOf(s).Elem(), reflect.ValueOf(c).Elem()
	for i := 0; i < sv.NumField(); i++ {
		f := sv.Type().Field(i)
		if k := f.Type.Kind(); (k == reflect.Map || k == reflect.Slice) && !sv.Field(i).IsNil() && sv.Field(i).Pointer() == cv.Field(i).Pointer() {
			t.Errorf("%s 與複本共用", f.Name)
		}
	}
	c.Picked["ㄇㄚˊ"][0] = "嘛"
	c.Pronouns["他"].Cues["學生"] = PronounCount{}
	if s.Picked["ㄇㄚˊ"][0] != "麻" || len(s.Pronouns["他"].Cues) != 1 {
		t.Fatal("修改複本影響到原本的資料")
	}
}
