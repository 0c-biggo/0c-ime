import Foundation

var checks = 0
func check(_ condition: Bool, _ message: String) {
    checks += 1
    precondition(condition, message)
}
let queue = RequestQueue()
for key in ["a", "8", "6"] { queue.append(Request(type: "key", key: key), characters: key) }
var seen: [String] = []
try queue.drain { item in
    seen.append(item.request.key ?? item.request.type)
    if seen.count == 1 {
        check(queue.isProcessing, "render callback must see the processing guard")
        queue.append(Request(type: "commit"))
        queue.append(Request(type: "mode", on: true))
        queue.append(Request(type: "key", key: "q"), characters: "q")
        try queue.drain { _ in fatalError("recursive drain must never send a request") }
    }
}
check(seen == ["a", "8", "6", "commit", "mode", "q"], "reentrant requests must follow all older keys")
check(queue.isEmpty && !queue.isProcessing, "successful drain must release the guard")

queue.append(Request(type: "key", key: "a"), characters: "a")
do {
    try queue.drain { _ in
        queue.append(Request(type: "key", key: "8"), characters: "8")
        throw EngineError.starting
    }
    fatalError("starting must propagate")
} catch EngineError.starting {}
seen = []
try queue.drain { seen.append($0.request.key!) }
check(seen == ["a", "8"], "unsent starting request must remain ahead of newly queued keys")

queue.append(Request(type: "commit"))
do {
    try queue.drain { _ in throw EngineError.unavailable }
} catch EngineError.unavailable { queue.clear() }
check(queue.isEmpty, "ambiguous failures must be discarded, never automatically replayed")

queue.append(Request(type: "key", key: "old"))
queue.append(Request(type: "commit"))
try queue.drain { _ in
    queue.clear() // focus loss or secure input during a host callback
    queue.append(Request(type: "key", key: "new"))
}
check(queue.count == 1, "cancellation must not remove or drain a new generation")
seen = []
try queue.drain { seen.append($0.request.key!) }
check(seen == ["new"], "only the new generation may resume")
queue.perform {
    check(queue.isProcessing, "immediate requests must also guard their render callbacks")
    queue.append(Request(type: "commit"))
}
check(!queue.isProcessing && queue.count == 1, "immediate reentry must remain queued")
queue.clear()

check(queue.canPassNamedKey(composing: false), "idle named keys belong to the app")
for request in [Request(type: "mode", on: true), Request(type: "toggle")] {
    queue.append(request)
    check(queue.canPassNamedKey(composing: false), "a cold-start mode change cannot consume a named key")
    check(!queue.canPassNamedKey(composing: true), "visible composition still needs named keys after a mode change")
    queue.append(Request(type: "commit")) // Caps Lock can commit before another queued mode change.
    check(queue.canPassNamedKey(composing: false), "an empty commit does not create composition")
    queue.perform {
        check(!queue.canPassNamedKey(composing: false), "in-flight callbacks must keep request order")
    }
    queue.append(Request(type: "key", key: "a"), characters: "a")
    check(!queue.canPassNamedKey(composing: false), "an older text key may create composition before Enter")
    queue.clear()
}
queue.append(Request(type: "select", index: 0))
check(!queue.canPassNamedKey(composing: false), "unknown composition-changing requests must remain ordered")
queue.clear()
queue.perform {
    check(!queue.canPassNamedKey(composing: false), "an unknown reply must still guard callbacks")
    queue.present {
        check(queue.canPassNamedKey(composing: false), "Enter during completed commit belongs to the app")
        check(!queue.canPassNamedKey(composing: true), "a reply with remaining composition still needs Enter")
        queue.append(Request(type: "key", key: "a"), characters: "a")
        check(!queue.canPassNamedKey(composing: false), "new input queued by a callback must precede Enter")
    }
}
queue.clear()
queue.append(Request(type: "key", key: "Enter"))
try queue.drain { _ in
    queue.present {
        check(queue.canPassNamedKey(composing: false), "the delivered replay head cannot consume the next Enter")
        queue.clear()
        check(!queue.canPassNamedKey(composing: false), "cancellation invalidates the presenting reply")
    }
}
queue.append(Request(type: "key", key: "Enter"))
queue.append(Request(type: "key", key: "a"), characters: "a")
try queue.drain { _ in
    queue.present {
        check(!queue.canPassNamedKey(composing: false), "older pending text must prevent premature host commands")
        queue.clear()
    }
}

// Cold English startup must hand back Enter/Tab before they can be deferred.
let coldEnglish = RequestQueue()
coldEnglish.append(Request(type: "mode", on: true))
for k in ["h", "e", "l"] { coldEnglish.append(Request(type: "key", key: k), characters: k) }
check(coldEnglish.englishLiteral(initial: "", english: false) == "hel", "literal queued English can precede a native host command")
let keypadEnglish = RequestQueue()
keypadEnglish.append(Request(type: "mode", on: true))
for k in ["a", "KP1", "KP."] { keypadEnglish.append(Request(type: "key", key: k), characters: k) }
check(keypadEnglish.englishLiteral(initial: "", english: false) == "a1.", "queued keypad keys hand back their character (#75)")
coldEnglish.perform {
    check(coldEnglish.englishLiteral(initial: "", english: false) == nil, "in-flight English cannot be speculatively replayed")
}
coldEnglish.append(Request(type: "key", key: "Tab"))
check(coldEnglish.englishLiteral(initial: "", english: false) == nil, "candidate selection must be resolved by engine")
// caps_lock（#219）：開啟暫時英文，關閉回到開啟前的模式；期間切換過就照切換後的。
func capsQueue(_ requests: [Request]) -> RequestQueue {
    let queue = RequestQueue()
    for request in requests { queue.append(request, characters: request.key) }
    return queue
}
let capsOn = Request(type: "caps_lock", on: true), capsOff = Request(type: "caps_lock", on: false)
let keyA = Request(type: "key", key: "A"), keyB = Request(type: "key", key: "b")
check(capsQueue([capsOn, keyA]).englishLiteral(initial: "", english: false) == "A", "Caps Lock queued from Zhuyin types English")
check(capsQueue([capsOn, keyA, capsOn, keyA]).englishLiteral(initial: "", english: false) == "AA", "a repeated Caps report changes nothing")
check(capsQueue([capsOn, keyA, capsOff, keyB]).englishLiteral(initial: "", english: false) == nil,
      "Caps release back to Zhuyin after English text is resolved by the engine")
check(capsQueue([capsOn, capsOff, keyB]).englishLiteral(initial: "", english: false) == nil, "Caps release restores Zhuyin")
check(capsQueue([capsOn, keyA, capsOff, keyB]).englishLiteral(initial: "", english: true) == "Ab", "Caps release keeps English from before")
check(capsQueue([capsOn, Request(type: "toggle"), Request(type: "toggle"), capsOff, keyB]).englishLiteral(initial: "", english: false) == "b",
      "a toggle while Caps is on wins over the mode from before")
check(capsQueue([capsOff, keyB]).englishLiteral(initial: "", english: true) == nil, "a release without its press is not guessed")
check(capsQueue([capsOn, Request(type: "mode", on: true), capsOff, keyB]).englishLiteral(initial: "", english: false) == nil,
      "mode makes the engine forget Caps Lock; the release is not guessed")
let capsNamed = capsQueue([capsOn])
check(capsNamed.canPassNamedKey(composing: false), "a queued Caps report cannot consume a named key")
let coldChinese = RequestQueue()
coldChinese.append(Request(type: "key", key: "h"), characters: "h")
check(coldChinese.englishLiteral(initial: "", english: false) == nil, "Chinese startup retains existing ordered replay")
// 具名鍵不是文字：冷啟動排隊中的 Backspace／Enter 不能被當成英文字補送給 App（#46、#95）。
for named in ["Backspace", "Enter", "Escape", "Delete", "Left", "Right", "Up", "Down", "PageUp", "PageDown"] {
    let queuedNamed = RequestQueue()
    queuedNamed.append(Request(type: "mode", on: true))
    queuedNamed.append(Request(type: "key", key: named))
    check(queuedNamed.englishLiteral(initial: "", english: false) == nil, "queued \(named) is not English text")
}

let presentingEnglish = RequestQueue()
presentingEnglish.append(Request(type: "key", key: "l"), characters: "l")
presentingEnglish.append(Request(type: "key", key: "p"), characters: "p")
try presentingEnglish.drain { _ in
    presentingEnglish.present {
        check(presentingEnglish.englishLiteral(initial: "hel", english: true) == "help", "a known reply must not duplicate the current English key")
        presentingEnglish.clear()
    }
}
check(presentingEnglish.isEmpty, "native command fallback cancels the old replay generation")

// A synchronous send uses perform without a queue head. Enter reentered from
// setMarkedText must still deliver the known English reply before native Enter.
let synchronousEnglish = RequestQueue()
check(synchronousEnglish.englishLiteral(initial: "hel", english: true, pending: true) == nil,
      "ordinary idle boundaries should still use the engine")
synchronousEnglish.perform {
    check(synchronousEnglish.englishLiteral(initial: "hel", english: true, pending: true) == nil,
          "an empty queue cannot speculate about an unknown synchronous reply")
    synchronousEnglish.present {
        check(synchronousEnglish.isEmpty, "synchronous delivery has no queued request head")
        check(synchronousEnglish.englishLiteral(initial: "hel", english: true, pending: true) == "hel",
              "native Enter during synchronous English presentation must release the known prefix")
        check(synchronousEnglish.englishLiteral(initial: "hel", english: true) == nil,
              "empty-queue fallback requires an owned English word")
        check(synchronousEnglish.englishLiteral(initial: "hel", english: false, pending: true) == nil,
              "Chinese composition must never take the English fallback")
        synchronousEnglish.append(Request(type: "key", key: "p"), characters: "p")
        check(synchronousEnglish.englishLiteral(initial: "hel", english: true, pending: true) == "help",
              "pending keys after a synchronous reply must be delivered once in order")
        synchronousEnglish.append(Request(type: "select", index: 0))
        check(synchronousEnglish.englishLiteral(initial: "hel", english: true, pending: true) == nil,
              "unresolved candidate selections cannot pass the boundary early")
        synchronousEnglish.clear()
        check(synchronousEnglish.englishLiteral(initial: "hel", english: true, pending: true) == nil,
              "focus cancellation invalidates even an empty synchronous presenting queue")
    }
}

print("RequestQueue: \(checks) checks passed")
