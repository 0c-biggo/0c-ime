import Foundation

/// 游標前的文字：已確認前文（InputController.confirmDocumentContext）與「游標前一個字是阿拉伯數字」（#337）共用同一套讀法。
/// 只讀游標（無選取）前面幾個 UTF-16 單位拿來判斷，讀到的文字不送給引擎、不記錄。讀取由呼叫端給
/// （IMKTextInput 的 attributedSubstring(from:)），這裡只有邏輯，方便測試。
enum CaretText {
    /// 游標前 length 個 UTF-16 單位。App 回報不出游標（NSNotFound）、有選取範圍、游標前不足 length 個、
    /// 讀不到或讀回的長度不對時 nil。
    static func before(selection: NSRange, length: Int, read: (NSRange) -> String?) -> String? {
        guard length > 0, selection.location != NSNotFound, selection.length == 0, selection.location >= length,
              let text = read(NSRange(location: selection.location - length, length: length)),
              (text as NSString).length == length else { return nil }
        return text
    }

    /// 游標前一個字是不是阿拉伯數字（#337）。文件開頭是 false；App 回報不出游標、有選取範圍或讀不到時 nil
    /// （不送，引擎照它自己記的）。同 Windows 外殼的 context::digit_before。
    static func digitBefore(selection: NSRange, read: (NSRange) -> String?) -> Bool? {
        guard selection.location != NSNotFound, selection.length == 0 else { return nil }
        if selection.location == 0 { return false }
        guard let unit = before(selection: selection, length: 1, read: read)?.utf16.first else { return nil }
        return isDigit(unit)
    }

    /// 半形 0–9 或全形 ０–９。
    static func isDigit(_ unit: UInt16) -> Bool { (0x30...0x39).contains(unit) || (0xFF10...0xFF19).contains(unit) }
}
