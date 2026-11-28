package settings

// keywords 是設定程式搜尋用的關鍵字（Item.Keywords，#363）：使用者找這一項時會打、但名稱、說明、選項名稱與
// 選項提示裡都沒有的詞（同義詞、別的輸入法的叫法、相關的鍵名）。已經寫在那些文字裡的詞不必列，搜尋本來就找得到。
// 每項 0–6 個、每個不超過 8 個字，各平台相同；規則由 TestKeywords* 把關。
// 新增設定時想一想使用者會怎麼稱呼它，補在這裡；沒有就不必列。
func keywords(key string) []string {
	switch key {
	case "selection":
		return []string{"AI", "智慧"}
	case "punctuation":
		return []string{"自動標點", "逗號", "句號", "AI"}
	case "document-context":
		return []string{"上下文", "前後文", "語境"}
	case "mixed-input":
		return []string{"混輸", "免切換"}
	case "english-autocomplete":
		return []string{"自動完成", "補完", "聯想"}
	case "toggle-key":
		return []string{"中文", "快速鍵", "熱鍵"}
	case "start-mode":
		return []string{"預設", "中文", "啟動"}
	case "mode-memory":
		return []string{"記憶", "全域", "中文"}
	case "esc-clear":
		return []string{"取消", "Escape", "刪除"}
	case "space-selects":
		return []string{"Space", "候選"}
	case "candidate-cursor":
		return []string{"選字", "前面", "後面"}
	case "advance-after-select":
		return []string{"候選", "移動"}
	case "candidate-layout":
		return []string{"候選窗", "直式", "橫式", "垂直", "水平"}
	case "output-simplified":
		return []string{"簡中", "繁簡", "簡化字"}
	case "simplified-hotkey":
		return []string{"簡中", "繁簡", "快速鍵", "熱鍵"}
	case "emoji-shortcut":
		return []string{"表情", "繪文字", "等號"}
	case "emoji-equals-first":
		return []string{"Emoji"}
	case "keyboard-layout":
		return []string{"配置", "鍵位", "Layout", "xiaohe"}
	case "selection-keys":
		return []string{"候選鍵", "asdf", "qwer"}
	case "ctrl-punctuation":
		return []string{"半形", "逗號", "句號", "Control"}
	case "caps-lock-english":
		return []string{"大寫", "CapsLock"}
	case "toneless":
		return []string{"免聲調", "無聲調", "音調", "拼音"}
	case "shift-symbol-halfwidth":
		return []string{"全半形", "英文標點", "括號"}
	case "cjk-latin-space":
		return []string{"空白", "間距", "排版", "盤古"}
	case "zhuyin-error-hint":
		return []string{"嗶聲", "音效", "錯誤", "聲音"}
	case "shift-tap-ms":
		return []string{"中英切換", "延遲", "靈敏度", "誤觸"}
	case "personal-learning":
		return []string{"記憶", "隱私", "不學習", "個人化"}
	// 四組模糊音在設定程式合成一個開關，關鍵字也合在一起顯示。
	case "fuzzy-z-zh":
		return []string{"捲舌", "ㄓㄗ"}
	case "fuzzy-c-ch":
		return []string{"捲舌", "ㄔㄘ"}
	case "fuzzy-s-sh":
		return []string{"捲舌", "ㄕㄙ"}
	case "fuzzy-en-eng":
		return []string{"鼻音", "ㄣㄥ"}
	case "style-shift-space":
		return []string{"Space", "空格"}
	case "style-tab":
		return []string{"斷句", "補字"}
	case "style-up":
		return []string{"方向鍵", "上鍵"}
	case "style-candidate-tab":
		return []string{"選字"}
	case "style-candidate-space":
		return []string{"Space", "空格", "翻頁"}
	case "style-candidate-enter":
		return []string{"Return", "確認"}
	case "style-page-size":
		return []string{"選字", "數量"}
	case "style-punctuation":
		return []string{"微軟注音", "逗號", "句號"}
	case "style-shift-letter":
		return []string{"大小寫", "臨時英文"}
	case "style-shift-delete":
		return []string{"刪除"}
	case "style-caps-letter":
		return []string{"CapsLock", "大寫鎖定"}
	case "style-candidate-navigation":
		return []string{"箭頭鍵", "上下左右"}
	}
	return nil
}
