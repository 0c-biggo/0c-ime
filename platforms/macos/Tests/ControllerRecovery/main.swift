import AppKit
import Foundation

var client = TextClient()
var controller = LingxiInputController(client: client)
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
let passed = capsOK && compositionOK && coldOK
let output: [String: Any] = ["passed": passed, "caps_recovery": capsOK, "composition_preserved_once": compositionOK,
                          "cold_queue": coldOK, "timeout_seconds": elapsed, "steps": results]
print(String(data: try JSONSerialization.data(withJSONObject: output, options: [.sortedKeys]), encoding: .utf8)!)
exit(passed ? 0 : 1)
