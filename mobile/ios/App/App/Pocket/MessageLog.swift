import Foundation

// MessageLog keeps every answer of the recorder in order ("MCU&<answer>&<value>"), so answers
// that come on their own (WIFIS changes, MCU&OFF) can be waited for after a mark(): see
// waitFor. The Bluetooth connection adds each notification of the command characteristic and
// tells when it's gone. Thread-safe: the heartbeat and polling write from other threads.
// The port of mobile/android/…/pocket/MessageLog.java.
final class MessageLog {
    private let condition = NSCondition()
    private var messages: [String] = []
    private var disconnected = false

    // split splits a notification that carries several answers ("MCU&WIFIOMCU&OFF").
    static func split(_ text: String) -> [String] {
        let cleaned = text.replacingOccurrences(of: "\0", with: "")
        var starts: [String.Index] = [cleaned.startIndex]
        var from = cleaned.startIndex
        while from < cleaned.endIndex, let found = cleaned.range(of: "MCU&", range: from..<cleaned.endIndex) {
            if found.lowerBound != cleaned.startIndex { starts.append(found.lowerBound) }
            from = found.upperBound
        }
        var parts: [String] = []
        for (i, start) in starts.enumerated() {
            let end = i + 1 < starts.count ? starts[i + 1] : cleaned.endIndex
            let part = cleaned[start..<end].trimmingCharacters(in: .whitespacesAndNewlines)
            if !part.isEmpty { parts.append(part) }
        }
        return parts
    }

    // valueOf is the value of an answer "MCU&<answer>&<value>" ("" for a bare "MCU&<answer>"),
    // or nil if text is another answer.
    static func valueOf(_ text: String, _ answer: String) -> String? {
        let exact = "MCU&" + answer
        if text == exact { return "" }
        return text.hasPrefix(exact + "&") ? String(text.dropFirst(exact.count + 1)) : nil
    }

    // add keeps the answers of one notification.
    func add(_ notification: String) {
        condition.lock()
        messages.append(contentsOf: Self.split(notification))
        condition.broadcast()
        condition.unlock()
    }

    // disconnect marks the connection as gone: waiting ends with 'disconnected'.
    func disconnect() {
        condition.lock()
        disconnected = true
        condition.broadcast()
        condition.unlock()
    }

    var isDisconnected: Bool {
        condition.lock()
        defer { condition.unlock() }
        return disconnected
    }

    func mark() -> Int {
        condition.lock()
        defer { condition.unlock() }
        return messages.count
    }

    // waitFor returns the value of the first answer to answer after since that accept takes,
    // or nil after timeout ms; it throws 'disconnected' when the recorder disconnects. accept
    // runs outside the lock: it may look at the log itself (PocketSession.collect).
    func waitFor(_ answer: String, since: Int, timeout: Int64, accept: (String) -> Bool) throws -> String? {
        let end = Clock.now() + timeout
        let interrupt = Interrupt.current
        var index = since
        while true {
            condition.lock()
            let fresh = index < messages.count ? Array(messages[index...]) : []
            index = max(index, messages.count)
            let gone = disconnected
            condition.unlock()
            for message in fresh {
                if let value = Self.valueOf(message, answer), accept(value) { return value }
            }
            if gone { throw PocketError("disconnected", "The recorder disconnected") }
            if interrupt.isRaised { throw PocketError("cancelled") }
            let left = end - Clock.now()
            if left <= 0 { return nil }
            condition.lock()
            if messages.count <= index && !disconnected {
                condition.wait(until: Date(timeIntervalSinceNow: Double(min(left, 100)) / 1000))
            }
            condition.unlock()
        }
    }

    // values are the values of all answers to answer after since, in order.
    func values(_ answer: String, since: Int) -> [String] {
        condition.lock()
        defer { condition.unlock() }
        guard since < messages.count else { return [] }
        return messages[since...].compactMap { Self.valueOf($0, answer) }
    }
}
