import AppKit
import Foundation

var client = TextClient()
var controller = LingxiInputController(client: client)
func capsChanged(_ on: Bool) {
    let event = CGEvent(keyboardEventSource: nil, virtualKey: 57, keyDown: true)!
    event.type = .flagsChanged
    event.flags = on ? .maskAlphaShift : []
    _ = controller.handle(NSEvent(cgEvent: event)!, client: client)
}

// #146: Caps release restores the engine's shared mode, which can still be
// English. Exercise the real controller and engine rather than mapping a bool
// to a label in isolation.
let peer = ControllerTestHost.session(inputAllowed: { true })
_ = try peer.handle(Request(type: "toggle"))
CandidatePanel.hints = []
capsChanged(true)
capsChanged(false)
let englishHints = CandidatePanel.hints
let englishHintOK = englishHints == ["英", "英"]
_ = try peer.handle(Request(type: "toggle"))
CandidatePanel.hints = []
capsChanged(true)
capsChanged(false)
let chineseHints = CandidatePanel.hints
let chineseHintOK = chineseHints == ["英", "中"]
peer.reset()
controller.inputControllerWillClose()
client = TextClient()
controller = LingxiInputController(client: client)
func input(caps: Bool) -> Bool {
    // Synthetic fixture, never an event from a real keyboard or user application.
    let event = NSEvent.keyEvent(with: .keyDown, location: .zero,
                                modifierFlags: caps ? [.capsLock] : [], timestamp: 0,
                                windowNumber: 0, context: nil, characters: "5",
                                charactersIgnoringModifiers: "5", isARepeat: false, keyCode: 23)!
    return controller.handle(event, client: client)
}
var results: [[String: Any]] = []
func record(_ name: String, _ handled: Bool) {
    results.append(["step": name, "handled": handled, "marked_length": client.markedLength,
                    "inserted_length": client.insertedLength])
}
record("caps_warm", input(caps: true))
try Data().write(to: URL(fileURLWithPath: CommandLine.arguments[2]))
let before = ProcessInfo.processInfo.systemUptime
record("warm_timeout", input(caps: true))
let elapsed = ProcessInfo.processInfo.systemUptime - before
record("caps_recovered", input(caps: true))
record("caps_repeated", input(caps: true))
record("caps_released", input(caps: false))
controller.inputControllerWillClose()
let warmOK = results[0]["handled"] as? Bool == false && results[0]["marked_length"] as? Int == 0
let failedOK = results[1]["handled"] as? Bool == false && elapsed >= 0.8 && elapsed < 2
let recoveredOK = results[2]["handled"] as? Bool == false && results[2]["marked_length"] as? Int == 0
let repeatedOK = results[3]["handled"] as? Bool == false && results[3]["marked_length"] as? Int == 0
let releasedOK = results[4]["handled"] as? Bool == true && results[4]["marked_length"] as? Int == 1
let noUnexpectedCommit = results.allSatisfy { $0["inserted_length"] as? Int == 0 }
let capsOK = warmOK && failedOK && recoveredOK && repeatedOK && releasedOK && noUnexpectedCommit

// A failed request must preserve the composition already shown to the host,
// exactly once, without retrying an ambiguously processed request.
client = TextClient()
controller = LingxiInputController(client: client)
record("composition_warm", input(caps: false))
let visible = client.marked
try Data().write(to: URL(fileURLWithPath: CommandLine.arguments[2]))
let failedHandled = input(caps: false)
record("composition_timeout", failedHandled)
let preserved = !visible.isEmpty && !failedHandled && client.inserted == [visible] && client.marked.isEmpty
let recoveredHandled = input(caps: false)
record("composition_recovered", recoveredHandled)
let compositionOK = preserved && recoveredHandled && !client.marked.isEmpty && client.inserted == [visible]
controller.inputControllerWillClose()

// Cold startup queues the Caps mode change before its keys. The real run loop
// replays them only once the private listener becomes available.
ControllerTestHost.socketPath = CommandLine.arguments[3]
client = TextClient()
controller = LingxiInputController(client: client)
CandidatePanel.hints = []
capsChanged(true)
let coldHintOK = CandidatePanel.hints.isEmpty
let coldFirst = input(caps: true)
record("cold_first", coldFirst)
let coldSecond = input(caps: true)
record("cold_second", coldSecond)
let buffered = coldFirst && coldSecond && client.marked.isEmpty && client.inserted.isEmpty
try Data().write(to: URL(fileURLWithPath: CommandLine.arguments[4]))
let replayDeadline = Date(timeIntervalSinceNow: 5)
while client.inserted.count < 2 && Date() < replayDeadline {
    RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.02))
}
let replayed = client.insertedLength == 2 && client.inserted.count == 2 && client.marked.isEmpty
let coldRecovered = input(caps: true)
record("cold_recovered", coldRecovered)
let coldReleased = input(caps: false)
record("cold_released", coldReleased)
let coldOK = buffered && replayed && !coldRecovered && coldReleased && client.markedLength == 1 && client.insertedLength == 2
controller.inputControllerWillClose()

// #219: Caps release returns to the mode from before Caps was turned on, unless
// a single Shift switched modes while Caps was on. Per-field mode memory, so
// only this controller's own session decides (shared mode is covered above).
ControllerTestHost.socketPath = CommandLine.arguments[5]
var capsHeld = false
func shiftTap() {
    for flags: CGEventFlags in [.maskShift, []] {
        let event = CGEvent(keyboardEventSource: nil, virtualKey: 60, keyDown: true)!
        event.type = .flagsChanged
        event.flags = capsHeld ? flags.union(.maskAlphaShift) : flags
        // Synthetic events carry no time of their own; the tap rules compare press, release and the last toggle.
        event.timestamp = CGEventTimestamp(ProcessInfo.processInfo.systemUptime * 1_000_000_000)
        _ = controller.handle(NSEvent(cgEvent: event)!, client: client)
    }
    Thread.sleep(forTimeInterval: 0.6) // past the duplicate-press window after a toggle
}
func nativeScenario(_ steps: [String]) -> [String: Any] {
    client = TextClient()
    controller = LingxiInputController(client: client)
    capsHeld = false
    CandidatePanel.hints = []
    for step in steps {
        switch step {
        case "shift": shiftTap()
        case "caps+": capsHeld = true; capsChanged(true)
        default: capsHeld = false; capsChanged(false)
        }
    }
    let typedZhuyin = input(caps: false) // "5" is ㄓ in Zhuyin, handed back to the app in English
    controller.inputControllerWillClose()
    return ["steps": steps.joined(separator: " "), "hints": CandidatePanel.hints, "zhuyin_after": typedZhuyin]
}
let nativeCases: [([String], String)] = [
    (["shift", "caps+", "caps-"], "英"),          // English before Caps stays English
    (["caps+", "caps-"], "中"),                   // Zhuyin before Caps returns to Zhuyin (#84 unchanged)
    (["caps+", "shift", "shift", "caps-"], "英"), // switched back to English by hand while Caps was on
    (["shift", "caps+", "shift", "caps-"], "中"), // switched to Zhuyin by hand while Caps was on
    (["caps+", "shift", "caps-"], "中"),
]
var nativeResults: [[String: Any]] = []
var nativeOK = true
for (steps, want) in nativeCases {
    let result = nativeScenario(steps)
    nativeResults.append(result)
    let hints = result["hints"] as? [String] ?? []
    // Every case shows 英 right after Caps turns on (the step before it may be a Shift hint).
    let capsOnIndex = steps.firstIndex(of: "caps+")!
    let capsOnHint = hints.count == steps.count && hints[capsOnIndex] == "英"
    nativeOK = nativeOK && capsOnHint && hints.last == want && (result["zhuyin_after"] as? Bool) == (want == "中")
}

let passed = capsOK && compositionOK && coldOK && englishHintOK && chineseHintOK && coldHintOK && nativeOK
let output: [String: Any] = ["passed": passed, "caps_recovery": capsOK, "composition_preserved_once": compositionOK,
                          "native_caps_restore": nativeOK, "native_cases": nativeResults,
                          "cold_queue": coldOK, "timeout_seconds": elapsed, "steps": results,
                          "english_hints": englishHints, "chinese_hints": chineseHints,
                          "cold_hint_suppressed": coldHintOK]
print(String(data: try JSONSerialization.data(withJSONObject: output, options: [.sortedKeys]), encoding: .utf8)!)
exit(passed ? 0 : 1)
