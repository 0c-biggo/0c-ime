import AppKit

// Headless host substitutes only: the production controller and IPC code run unchanged.
protocol IMKTextInput: AnyObject {
    func bundleIdentifier() -> String!
    func overrideKeyboard(withKeyboardNamed: String!)
    func insertText(_ text: Any!, replacementRange: NSRange)
    func selectedRange() -> NSRange
    func markedRange() -> NSRange
    func attributedSubstring(from: NSRange) -> NSAttributedString!
    func attributes(forCharacterIndex: Int, lineHeightRectangle: UnsafeMutablePointer<NSRect>!) -> [AnyHashable: Any]!
    func firstRect(forCharacterRange: NSRange, actualRange: NSRangePointer!) -> NSRect
    func windowLevel() -> Int32
    func setMarkedText(_ text: Any!, selectionRange: NSRange, replacementRange: NSRange)
}
class IMKInputController: NSObject {
    private let textClient: IMKTextInput
    init(client: IMKTextInput) { textClient = client; super.init() }
    func client() -> IMKTextInput! { textClient }
    func recognizedEvents(_ sender: Any!) -> Int { 0 }
    func handle(_ event: NSEvent!, client sender: Any!) -> Bool { false }
    func menu() -> NSMenu! { nil }
    func activateServer(_ sender: Any!) {}
    func deactivateServer(_ sender: Any!) {}
    func commitComposition(_ sender: Any!) {}
    func hidePalettes() {}
    func inputControllerWillClose() {}
}
final class CandidatePanel {
    static var hints: [String] = []
    var choose: ((Int) -> Void)?
    var level = NSWindow.Level.popUpMenu
    func orderOut(_ sender: Any?) {}
    func show(_ snapshot: Snapshot, at: NSRect) {}
    func showHint(_ text: String, at: NSRect, for duration: TimeInterval = 1) { Self.hints.append(text) }
}
final class RetireMonitor { static let shared = RetireMonitor(); func noteKey() {} }
final class ModelStatusMonitor {
    static let shared = ModelStatusMonitor()
    var labels: [String] { [] }
    func refresh() {}
    func refreshForMenu() {}
}
final class UpdateMenuMonitor {
    struct Status { var outputSimplified: Bool? }
    static let shared = UpdateMenuMonitor()
    static let menuRefreshInterval: TimeInterval = 2
    var status: Status?
    func refreshForMenu() {}
    func append(to: NSMenu, target: AnyObject, action: Selector) {}
    func setSimplified(_ on: Bool) {}
    func perform(_ item: Int) {}
    static func selectedItem(from: Any?) -> Int? { nil }
}
final class UpdateAgentMonitor {
    enum Status { case requiresApproval, enabled, missing }
    static let shared = UpdateAgentMonitor()
    var status: Status?
    func refreshStatus(minimumInterval: TimeInterval) {}
    static func openSettings() {}
}
enum ControllerTestHost {
    static var socketPath = CommandLine.arguments[1]
    static func session(inputAllowed: @escaping () -> Bool) -> EngineSession {
        EngineSession(connection: EngineConnection(path: socketPath), inputAllowed: inputAllowed)
    }
}
final class TextClient: IMKTextInput {
    var marked = ""
    var inserted: [String] = []
    var markedLength: Int { marked.utf16.count }
    var insertedLength: Int { inserted.reduce(0) { $0 + $1.utf16.count } }
    func bundleIdentifier() -> String! { "test.controller" }
    func overrideKeyboard(withKeyboardNamed: String!) {}
    func insertText(_ text: Any!, replacementRange: NSRange) {
        inserted.append((text as? String) ?? (text as? NSAttributedString)?.string ?? "")
        marked = ""
    }
    func selectedRange() -> NSRange { NSRange(location: 0, length: 0) }
    func markedRange() -> NSRange { NSRange(location: markedLength == 0 ? NSNotFound : 0, length: markedLength) }
    func attributedSubstring(from: NSRange) -> NSAttributedString! { nil }
    func attributes(forCharacterIndex: Int, lineHeightRectangle: UnsafeMutablePointer<NSRect>!) -> [AnyHashable: Any]! { [:] }
    func firstRect(forCharacterRange: NSRange, actualRange: NSRangePointer!) -> NSRect { .zero }
    func windowLevel() -> Int32 { 0 }
    func setMarkedText(_ text: Any!, selectionRange: NSRange, replacementRange: NSRange) {
        marked = (text as? NSAttributedString)?.string ?? ""
    }
}
