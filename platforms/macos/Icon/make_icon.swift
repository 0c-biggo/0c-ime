// 產生選單列的輸入法圖示（範本圖：只有黑與透明，系統依選單列深淺自動上色）。
// 用法：swift make_icon.swift <輸出.tiff> [預覽.png]
// 比照系統輸入法圖示的橫向圓角方塊（22×16 pt，含 1x 與 2x）：左邊鏤空「注」，右邊鏤空「0c」。
import AppKit

func draw(scale: CGFloat) -> NSBitmapImageRep {
    let w: CGFloat = 22, h: CGFloat = 16
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(w * scale), pixelsHigh: Int(h * scale), bitsPerSample: 8,
                               samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                               colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    rep.size = NSSize(width: w, height: h)
    NSGraphicsContext.saveGraphicsState()
    let ctx = NSGraphicsContext(bitmapImageRep: rep)!
    NSGraphicsContext.current = ctx
    NSColor.black.setFill()
    NSBezierPath(roundedRect: NSRect(x: 0.5, y: 0.5, width: w - 1, height: h - 1), xRadius: 3.5, yRadius: 3.5).fill()
    ctx.compositingOperation = .destinationOut
    let main = NSAttributedString(string: "注", attributes: [
        .font: NSFont(name: "PingFangTC-Semibold", size: 10.5) ?? NSFont.boldSystemFont(ofSize: 10.5),
        .foregroundColor: NSColor.black])
    let m = main.size()
    main.draw(at: NSPoint(x: 1.7, y: (h - m.height) / 2))
    let mark = NSAttributedString(string: "0c", attributes: [
        .font: NSFont.systemFont(ofSize: 6.2, weight: .heavy), .foregroundColor: NSColor.black, .kern: -0.2])
    let k = mark.size()
    mark.draw(at: NSPoint(x: w - 1.6 - k.width, y: 2.8))
    NSGraphicsContext.restoreGraphicsState()
    return rep
}

let args = CommandLine.arguments
let reps = [draw(scale: 1), draw(scale: 2)]
let image = NSImage(size: NSSize(width: 22, height: 16))
reps.forEach { image.addRepresentation($0) }
try! image.tiffRepresentation(using: .lzw, factor: 0)!.write(to: URL(fileURLWithPath: args[1]))
if args.count > 2 {
    // 預覽：放大 16 倍，左邊白底黑圖、右邊深底白圖（模擬淺色與深色選單列）
    let W: CGFloat = 352, H: CGFloat = 256
    let big = NSImage(size: NSSize(width: W, height: H))
    big.addRepresentation(draw(scale: 16))
    let out = NSImage(size: NSSize(width: W * 2, height: H))
    out.lockFocus()
    NSColor.white.setFill(); NSRect(x: 0, y: 0, width: W, height: H).fill()
    NSColor(white: 0.12, alpha: 1).setFill(); NSRect(x: W, y: 0, width: W, height: H).fill()
    big.draw(in: NSRect(x: 0, y: 0, width: W, height: H))
    let tinted = NSImage(size: NSSize(width: W, height: H))
    tinted.lockFocus()
    big.draw(in: NSRect(x: 0, y: 0, width: W, height: H))
    NSColor.white.set(); NSRect(x: 0, y: 0, width: W, height: H).fill(using: .sourceAtop)
    tinted.unlockFocus()
    tinted.draw(in: NSRect(x: W, y: 0, width: W, height: H))
    out.unlockFocus()
    let rep = NSBitmapImageRep(data: out.tiffRepresentation!)!
    try! rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: args[2]))
}
