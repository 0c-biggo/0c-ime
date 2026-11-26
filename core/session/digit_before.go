package session

import "github.com/0c-biggo/0c-ime/core/proto"

// 游標前一個字是阿拉伯數字（#337）：「3 隻」的 3 多半是英數模式（或數字鍵盤）打的，按鍵交還應用程式、不算前文，
// 選字模型沒有前文時把量詞換成最常見的同音字（26.9% 錯）。只記一個位元給選字模型（rerank.Request.DigitBefore），
// 不記數字本身；只在記憶體，不寫檔、不學習、不進快照（外殼的 trace 錄快照）。
//
// 記下：沒有組字時交還應用程式的按鍵是數字（主鍵盤的 0–9 只有英數模式會交還，數字鍵盤兩種模式都會），
// 或外殼讀到游標前一個字是數字（proto.Request.DigitBefore，Windows 英數模式的按鍵不經過核心）。
// 清掉：其他交還應用程式的按鍵（字母、空白、方向鍵、Backspace……）、提交或放棄組字（Esc 清掉整段、失焦、
// 外殼的重設都是 cancel／commit）、私人欄位、暫停（暫停中打的也不記）。切換中英不清：打完數字切回注音才是要的情況。
// 前文確認的清除（context on=false）也不清：Fcitx5 外殼每次切換中英、交還按鍵都會送一次，跟游標有沒有移動無關。
// 使用時另看前文設定、私人與暫停（digitBefore）。

// isDigitKey：交還應用程式時會打出阿拉伯數字的按鍵。
func isDigitKey(k string) bool {
	if c, ok := proto.KeypadChar(k); ok {
		return c >= '0' && c <= '9'
	}
	return len(k) == 1 && k[0] >= '0' && k[0] <= '9'
}

// noteDigitBefore 在請求處理完之後更新旗標；passed 是這個請求的按鍵交還了應用程式。
func (s *Session) noteDigitBefore(r proto.Request, passed bool) {
	switch {
	case r.Private || s.paused:
		s.digitLeft = false // 暫停中打的數字恢復後也不用
	case passed:
		s.digitLeft = r.Type == proto.Key && isDigitKey(r.Key)
	}
}

// digitBefore 回報要不要把旗標送給選字模型：外殼能確認前文（同 neuralContext）、使用者沒關前文，
// 而且不在私人、暫停或英數狀態。
func (s *Session) digitBefore() bool {
	return s.digitLeft && s.documentEnabled && !s.DocumentContextOff && !s.private && !s.paused && !s.english
}
