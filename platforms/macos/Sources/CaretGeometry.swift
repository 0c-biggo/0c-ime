import Foundation

enum CaretGeometry {
    // 行高至少 1 點。Chrome、Arc、VS Code 等 Chromium／Electron App 與 Warp 經 IMK 回傳的 firstRect
    // 是約 1.6e-314 的未初始化值：原點、高度極小但大於 0，若當成游標，候選窗會固定在主螢幕左下角。
    static func valid(_ rect: NSRect) -> Bool {
        [rect.origin.x, rect.origin.y, rect.width, rect.height].allSatisfy { $0.isFinite }
            && rect.height >= 1
    }

    static func resolve(cursor: NSRect, line: NSRect, screens: [NSRect]) -> NSRect? {
        func onScreen(_ rect: NSRect) -> Bool {
            valid(rect) && screens.contains {
                rect.minX >= $0.minX && rect.minX <= $0.maxX && rect.minY >= $0.minY && rect.minY <= $0.maxY
            }
        }
        // IMK 的 firstRect 在 TextEdit 回傳行上緣；Safari 則可能回傳不在任何螢幕
        // 內的矩形。只在座標有效時採用它，否則使用 attributes 的行下緣。
        if onScreen(cursor) {
            let rect = cursor.standardized
            return NSRect(x: rect.minX, y: rect.minY - rect.height, width: 1, height: rect.height)
        }
        if onScreen(line) {
            let rect = line.standardized
            return NSRect(x: rect.minX, y: rect.minY, width: 1, height: rect.height)
        }
        return nil
    }
}
