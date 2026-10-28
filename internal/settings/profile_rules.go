package settings

import (
	"strconv"

	"github.com/0c-biggo/0c-ime/core/inputstyle"
)

type ruleSpec struct {
	key, label, description string
	values                  []string
	labels                  map[string]string
	get                     func(inputstyle.Rules) string
	set                     func(*inputstyle.Rules, string)
}

func ruleSpecs() []ruleSpec {
	return []ruleSpec{
		{"style-shift-space", "Shift＋空白", "切換全半形時也包含英文與數字", []string{"disabled", "fullwidth", "space", "single"}, map[string]string{"disabled": "不使用", "fullwidth": "切換全半形", "space": "插入空白", "single": "開單字候選"}, func(r inputstyle.Rules) string { return r.ShiftSpace }, func(r *inputstyle.Rules, v string) { r.ShiftSpace = v }},
		{"style-tab", "組字時的 Tab", "不同風格可輪換字詞或調整斷詞；Shift＋Tab 可反向輪換字詞", []string{"0c", "cycle", "segment"}, map[string]string{"0c": "靈犀原有操作", "cycle": "輪換字詞", "segment": "調整斷詞"}, func(r inputstyle.Rules) string { return r.Tab }, func(r *inputstyle.Rules, v string) { r.Tab = v }},
		{"style-up", "組字時的 ↑", "尚未開候選窗時的上方向鍵", []string{"0c", "cycle"}, map[string]string{"0c": "靈犀原有操作", "cycle": "輪換字詞"}, func(r inputstyle.Rules) string { return r.Up }, func(r *inputstyle.Rules, v string) { r.Up = v }},
		{"style-candidate-tab", "候選窗的 Tab", "已顯示候選字時的 Tab 操作", []string{"length", "grid", "next"}, map[string]string{"length": "切換字詞長度", "grid": "展開多排候選", "next": "移到下一個候選"}, func(r inputstyle.Rules) string { return r.CandidateTab }, func(r *inputstyle.Rules, v string) { r.CandidateTab = v }},
		{"style-candidate-space", "候選窗的空白鍵", "候選窗開啟時按空白鍵", []string{"page", "select"}, map[string]string{"page": "下一頁", "select": "選取候選字"}, func(r inputstyle.Rules) string { return r.CandidateSpace }, func(r *inputstyle.Rules, v string) { r.CandidateSpace = v }},
		{"style-candidate-enter", "候選窗的 Enter", "確認候選字後是否同時送出整段組字", []string{"select", "commit"}, map[string]string{"select": "選字並保留組字", "commit": "選字並送出"}, func(r inputstyle.Rules) string { return r.CandidateEnter }, func(r *inputstyle.Rules, v string) { r.CandidateEnter = v }},
		{"style-page-size", "每頁候選數", "每頁顯示幾個候選字詞", []string{"9", "10"}, nil, func(r inputstyle.Rules) string { return strconv.Itoa(r.CandidatePageSize) }, func(r *inputstyle.Rules, v string) { r.CandidatePageSize, _ = strconv.Atoi(v) }},
		{"style-punctuation", "標點操作", "微軟參考反引號前綴；小麥與新酷音參考其標點規則，非原輸入法完整重現", []string{"0c", "microsoft", "standard", "chewing"}, map[string]string{"0c": "靈犀", "microsoft": "微軟參考", "standard": "小麥參考", "chewing": "新酷音參考"}, func(r inputstyle.Rules) string { return r.Punctuation }, func(r *inputstyle.Rules, v string) { r.Punctuation = v }},
		{"style-shift-letter", "Shift＋英文字母", "中文模式按 Shift＋字母時打大寫留在組字內、先送出組字再打大寫，或打小寫字母；中文模式不理 Caps Lock", []string{"inline", "commit", "lower"}, map[string]string{"inline": "留在組字內", "commit": "送出組字並輸入英文", "lower": "打小寫字母"}, func(r inputstyle.Rules) string { return r.ShiftLetter }, func(r *inputstyle.Rules, v string) { r.ShiftLetter = v }},
		{"style-shift-delete", "Shift＋Delete", "注音尚未輸完時，可只退回聲調", []string{"0c", "tone"}, map[string]string{"0c": "靈犀原有操作", "tone": "退回聲調"}, func(r inputstyle.Rules) string { return r.ShiftDelete }, func(r *inputstyle.Rules, v string) { r.ShiftDelete = v }},
		{"style-caps-letter", "Caps Lock 英文字母", "Caps Lock 暫時切英文時的字母大小寫；不會改變系統 Caps Lock 狀態", []string{"platform", "lower"}, map[string]string{"platform": "依平台大小寫", "lower": "預設小寫，Shift 大寫"}, func(r inputstyle.Rules) string { return r.CapsLetter }, func(r *inputstyle.Rules, v string) { r.CapsLetter = v }},
		{"style-candidate-navigation", "候選方向鍵", "水平操作為左右選候選、上下翻頁；排列外觀可另選", []string{"0c", "horizontal"}, map[string]string{"0c": "依候選排列", "horizontal": "水平操作"}, func(r inputstyle.Rules) string { return r.CandidateNavigation }, func(r *inputstyle.Rules, v string) { r.CandidateNavigation = v }},
	}
}
func lookupRule(key string) (ruleSpec, bool) {
	for _, sp := range ruleSpecs() {
		if sp.key == key {
			return sp, true
		}
	}
	return ruleSpec{}, false
}
func (s *Settings) setRule(sp ruleSpec, value string) error {
	st := s.store()
	for i := range st.Items {
		if st.Items[i].ID == st.Active {
			sp.set(&st.Items[i].Rules, value)
			if err := st.Items[i].Rules.Validate(); err != nil {
				return err
			}
		}
	}
	s.putStore(st)
	return nil
}
