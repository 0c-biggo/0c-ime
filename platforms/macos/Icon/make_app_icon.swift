// 產生 App 圖示 AppIcon.icns：與官網 favicon（https://0c.biggo.com/favicon.svg）相同的設計，
// 深青圓角方塊、白色等寬粗體「0C」與右下圓點。系統設定的背景項目、Finder 與通知都顯示這個圖示（#25）。
// 用法：swift make_app_icon.swift <輸出.icns>（需要 iconutil）
import AppKit

// favicon 的座標系是 64×64；macOS App 圖示的方塊占 1024 畫布的 824（四周留 100）。
func draw(pixels: Int) -> NSBitmapImageRep {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: pixels, pixelsHigh: pixels, bitsPerSample: 8,
                               samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                               colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    let s = CGFloat(pixels)
    rep.size = NSSize(width: s, height: s)
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    let inset = s * 100 / 1024, side = s - 2 * inset, unit = side / 64
    NSColor(srgbRed: 0, green: 0x5c / 255.0, blue: 0x73 / 255.0, alpha: 1).setFill()
    NSBezierPath(roundedRect: NSRect(x: inset, y: inset, width: side, height: side), xRadius: 14 * unit, yRadius: 14 * unit).fill()
    let font = NSFont.monospacedSystemFont(ofSize: 30 * unit, weight: .bold)
    let text = NSAttributedString(string: "0C", attributes: [.font: font, .foregroundColor: NSColor.white])
    let size = text.size()
    // SVG 的 text 以基線 y=44（由上往下）置中於 x=29。
    let baseline = inset + (64 - 44) * unit
    text.draw(at: NSPoint(x: inset + 29 * unit - size.width / 2, y: baseline + font.descender))
    NSColor.white.setFill()
    let r = 4 * unit
    NSBezierPath(ovalIn: NSRect(x: inset + 52 * unit - r, y: inset + (64 - 42) * unit - r, width: 2 * r, height: 2 * r)).fill()
    NSGraphicsContext.restoreGraphicsState()
    return rep
}

let output = URL(fileURLWithPath: CommandLine.arguments[1])
let iconset = FileManager.default.temporaryDirectory.appendingPathComponent("AppIcon-\(getpid()).iconset")
try? FileManager.default.removeItem(at: iconset)
try! FileManager.default.createDirectory(at: iconset, withIntermediateDirectories: true)
for points in [16, 32, 128, 256, 512] {
    for scale in [1, 2] {
        let name = scale == 1 ? "icon_\(points)x\(points).png" : "icon_\(points)x\(points)@2x.png"
        try! draw(pixels: points * scale).representation(using: .png, properties: [:])!.write(to: iconset.appendingPathComponent(name))
    }
}
let task = Process()
task.executableURL = URL(fileURLWithPath: "/usr/bin/iconutil")
task.arguments = ["-c", "icns", iconset.path, "-o", output.path]
try! task.run()
task.waitUntilExit()
try? FileManager.default.removeItem(at: iconset)
exit(task.terminationStatus)
