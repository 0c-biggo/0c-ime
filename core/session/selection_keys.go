package session

// defaultSelectionKeys 是候選窗預設的選字鍵：1..9,0 對應每一頁第 1..10 項。
const defaultSelectionKeys = "1234567890"

// selectionKeys 回傳實際使用的選字鍵（#112）：SelectionKeys 要剛好是每頁項數（10）個不重複的可見 ASCII 字元
// （不含大寫，那是 Shift＋字母），否則用預設。設定只開放 1234567890、1234qweras、asdfghjkl;，這裡只防不合用的值。
func (s *Session) selectionKeys() string {
	keys := s.SelectionKeys
	if len(keys) != s.pageSize() && !(s.pageSize() == 9 && len(keys) == 10) {
		return defaultSelectionKeys[:s.pageSize()]
	}
	for i := range len(keys) {
		c := keys[i]
		if c <= ' ' || c > '~' || c >= 'A' && c <= 'Z' {
			return defaultSelectionKeys[:s.pageSize()]
		}
		for j := range i {
			if keys[j] == c {
				return defaultSelectionKeys[:s.pageSize()]
			}
		}
	}
	return keys[:s.pageSize()]
}

// selectionKeyColumns 是多排候選（含 Emoji）每排最多幾個（#112）：選字鍵分在鍵盤幾排，候選就照著排。
// 1234qweras 每排 4 個，1234／qwer／as 對上鍵盤三排；1234567890、asdfghjkl; 在同一排，維持 5 個。
// 一頁照舊 10 個（4／4／2），翻頁不變；顏文字（2 個）與片語（直排）本來就比較少，不受影響。
func (s *Session) selectionKeyColumns() int {
	if s.selectionKeys() == "1234qweras" {
		return 4
	}
	return 5
}

// selectionIndex 回報按鍵選的是這一頁的第幾項（0 起算），不是選字鍵時為 -1。
// 選字鍵優先；數字 1–0 不論設定都照位置選字，舊外殼與自己畫候選窗的程式只顯示數字也能用。
func (s *Session) selectionIndex(k string) int {
	if len(k) != 1 {
		return -1
	}
	keys := s.selectionKeys()
	for i := range len(keys) {
		if keys[i] == k[0] {
			return i
		}
	}
	if k[0] >= '0' && k[0] <= '9' {
		if n := (int(k[0]-'0') + 9) % 10; n < s.pageSize() {
			return n
		}
		return -1 // // 1..9,0 → 第 0..9 項
	}
	return -1
}
