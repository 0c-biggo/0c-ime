import AppKit

enum KeyMapping {
    /// 數字鍵盤的數字與 . * + / - =（keyCode 與排列無關）；鍵盤 Enter（76）另外當 Enter。
    static let keypadCodes: Set<UInt16> = [65, 67, 69, 75, 78, 81, 82, 83, 84, 85, 86, 87, 88, 89, 91, 92]

    /// 0.2.0 加詞與反悔鍵（#123）：只按著 Shift 時的 ←→、⌫、Fn＋⌫（鍵碼 117）。Mac 筆電沒有獨立的 Delete 鍵，
    /// 候選窗的反悔鍵是 Shift＋⌫。核心在沒有對應功能時把它們當成原本的鍵，外殼不必追蹤候選窗。
    static let vocabCodes: [UInt16: String] = [123: "ShiftLeft", 124: "ShiftRight", 51: "ShiftBackspace", 117: "ShiftDelete"]

    /// `vocabKeys`：引擎回報 `vocab_keys`（認得上面的新鍵名）時才送；舊引擎或開關關閉時 Shift 照舊忽略，
    /// Shift＋←／→ 與 ←／→ 相同。
    static func key(code: UInt16, characters: String?, modifiers: NSEvent.ModifierFlags, vocabKeys: Bool = false, styleShiftSpace: Bool = false, capsLower: Bool = false) -> String? {
        guard modifiers.intersection([.command, .control, .option]).isEmpty else { return nil }
        if styleShiftSpace, code == 49, modifiers.contains(.shift) { return "ShiftSpace" }
        if vocabKeys, modifiers.contains(.shift), let key = vocabCodes[code] { return key }
        let named: [UInt16: String] = [36: "Enter", 76: "Enter", 48: "Tab", 51: "Backspace", 117: "Delete",
            53: "Escape", 123: "Left", 124: "Right", 125: "Down", 126: "Up",
            116: "PageUp", 121: "PageDown", 115: "Home", 119: "End"] // Home／End 含筆電的 Fn＋←／→（#106）
        // Shift+Tab 是獨立的 BackTab：組字中核心吃掉不動作，沒在組字時交還 App（#106）。
        if code == 48, modifiers.contains(.shift) { return "BackTab" }
        if let key = named[code] { return key }
        guard let characters, characters.unicodeScalars.count == 1,
              let scalar = characters.unicodeScalars.first, scalar.value >= 32, scalar.value <= 126 else { return nil }
        // 數字鍵盤送 "KP" 加上字元，核心不當注音鍵（#75）。
        if keypadCodes.contains(code), scalar.value > 32 { return "KP" + characters }
        // Caps Lock 是英文大寫模式（#84）：字母一律大寫；同 macOS 系統的大寫鎖定，按住 Shift 也還是大寫（不像 Windows 會反轉成小寫）。
        if modifiers.contains(.capsLock), scalar.properties.isAlphabetic {
            return capsLower && !modifiers.contains(.shift) ? characters.lowercased() : characters.uppercased()
        }
        return characters
    }

    /// Ctrl＋標點（#168）：只按著 Control（可以加 Shift，沒有 Command／Option）的 , . ' ; 與 ⇧/ ⇧1 對應的協定鍵名。
    /// 用鍵碼判斷：輸入法啟用時鍵盤固定是美式排列（overrideKeyboard），Control 會改變 characters。
    /// 要不要送由呼叫端決定（注音模式、引擎回報 ctrl_punctuation）。
    static func ctrlPunct(code: UInt16, modifiers: NSEvent.ModifierFlags) -> String? {
        guard modifiers.contains(.control), modifiers.intersection([.command, .option]).isEmpty else { return nil }
        let shift = modifiers.contains(.shift)
        switch (code, shift) {
        case (43, false): return "Ctrl+,"
        case (47, false): return "Ctrl+."
        case (39, false): return "Ctrl+'"
        case (41, false): return "Ctrl+;"
        case (44, true): return "Ctrl+?"
        case (18, true): return "Ctrl+!"
        default: return nil
        }
    }

    /// F11 的鍵碼（與排列無關）。
    static let f11Code: UInt16 = 103

    /// 切換「輸出簡體字」的 ⌃⇧F11（#192，settings.SimplifiedHotkeyLabel）：只按著 Control 與 Shift，沒有 Command／Option。
    /// F 鍵的事件本來就帶 .function（不論有沒有按 fn），不看它。要不要攔由呼叫端看引擎回報的 simplified_hotkey。
    static func simplifiedHotkey(code: UInt16, modifiers: NSEvent.ModifierFlags) -> Bool {
        code == f11Code && modifiers.contains([.control, .shift]) && modifiers.intersection([.command, .option]).isEmpty
    }

    /// Ctrl＋標點打出的全形標點：引擎冷啟動時排隊，重送後引擎沒處理（設定剛關掉）就補送這個字給 App。
    static let ctrlPunctText: [String: String] = ["Ctrl+,": "，", "Ctrl+.": "。", "Ctrl+'": "、", "Ctrl+;": "；", "Ctrl+?": "？", "Ctrl+!": "！"]

    /// 引擎沒處理這個鍵時要原樣補送給 App 的文字。只有單一可見字元才有；Enter、Backspace、方向鍵等
    /// 具名鍵沒有文字——把鍵名當文字送出，App 會收到 "Enter"、"Backspace" 這幾個英文字母。
    static func text(for key: String) -> String? {
        if key == "ShiftSpace" { return " " }
        if key.hasPrefix("KP"), key.unicodeScalars.count == 3 { return String(key.dropFirst(2)) }
        guard key.unicodeScalars.count == 1 else { return nil }
        return key
    }
}
