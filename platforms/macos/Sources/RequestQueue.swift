import Foundation

/// Main-thread requests remain ordered across synchronous IMKit callbacks.
/// The current item stays at the head until it succeeds; only `starting`
/// (nothing was sent) may retry it. Callers clear the queue on ambiguous errors.
final class RequestQueue {
    struct Item {
        let request: Request
        let characters: String?
    }
    private var items: [Item] = []
    private var generation = 0
    private var processingQueuedGeneration: Int?
    private(set) var isPresenting = false
    private(set) var isProcessing = false
    var cancellationGeneration: Int { generation }
    var isEmpty: Bool { items.isEmpty }
    var count: Int { items.count }

    /// Recover unsent ordinary English text without consuming a native host
    /// command. Never speculate about an in-flight request or Chinese input.
    func englishLiteral(initial: String, english: Bool, pending: Bool = false) -> String? {
        guard (!isEmpty || isPresenting && pending), !isProcessing || isPresenting else { return nil }
        let pending = items.dropFirst(isPresenting && processingQueuedGeneration == generation ? 1 : 0)
        var text = initial
        var mode = english
        // caps_lock（#219）：開啟暫時切到英文，關閉回到開啟前的模式；期間切換過（toggle、mode）就不再改。
        // 關閉前在這段排隊裡沒看到開啟時，不知道核心記得的模式，不猜。
        var capsBefore: Bool?
        var capsSeen = false
        for item in pending {
            switch item.request.type {
            case "caps_lock":
                if item.request.on == true {
                    guard !capsSeen else { continue } // 核心已記得開著，不再改模式
                    capsBefore = mode
                    capsSeen = true
                    mode = true
                } else {
                    guard capsSeen else { return nil }
                    if let before = capsBefore, before != mode {
                        guard text.isEmpty else { return nil }
                        mode = before
                    }
                    capsBefore = nil
                    capsSeen = false
                }
            case "mode", "toggle":
                guard text.isEmpty else { return nil }
                mode = item.request.type == "mode" ? item.request.on == true : !mode
                capsBefore = nil
                if item.request.type == "mode" { capsSeen = false } // mode 讓核心忘掉 Caps Lock 狀態
            case "key":
                // 數字鍵盤送 "KP" 加字元（#75），文字就是那個字元。
                guard mode, var key = item.request.key else { return nil }
                if key.hasPrefix("KP"), key.unicodeScalars.count == 3 { key = String(key.dropFirst(2)) }
                guard key.unicodeScalars.count == 1,
                      let scalar = key.unicodeScalars.first, (32...126).contains(scalar.value) else { return nil }
                text += key
            default: return nil
            }
        }
        return mode ? text : nil
    }

    /// A host callback can pass an idle named key once the current reply is
    /// being delivered, provided no older pending input can create composition.
    func canPassNamedKey(composing: Bool) -> Bool {
        guard !composing, !isProcessing || isPresenting else { return false }
        let pending = items.dropFirst(isPresenting && processingQueuedGeneration == generation ? 1 : 0)
        return pending.allSatisfy {
            ["mode", "toggle", "commit", "caps_lock"].contains($0.request.type)
        }
    }

    /// The engine reply is known and the host is receiving its text. Keep the
    /// processing guard for new input while allowing idle host commands.
    func present(_ action: () -> Void) {
        precondition(isProcessing && !isPresenting)
        isPresenting = true
        defer { isPresenting = false }
        action()
    }

    func append(_ request: Request, characters: String? = nil) {
        items.append(Item(request: request, characters: characters))
    }

    func clear() {
        generation += 1
        isPresenting = false
        items.removeAll()
    }

    func perform<T>(_ action: () throws -> T) rethrows -> T {
        precondition(!isProcessing)
        isProcessing = true
        defer { isProcessing = false }
        return try action()
    }

    func drain(_ consume: (Item) throws -> Void) throws {
        guard !isProcessing else { return }
        let activeGeneration = generation
        try perform {
            while activeGeneration == generation, let item = items.first {
                processingQueuedGeneration = activeGeneration
                defer { processingQueuedGeneration = nil }
                try consume(item)
                // A focus/security callback can invalidate the entire queue.
                guard activeGeneration == generation else { return }
                items.removeFirst()
            }
        }
    }
}
