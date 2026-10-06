import Foundation

// PocketError carries a code for the web app to explain (frontend/src/components/PocketUsbSync.tsx),
// the same codes the desktop and Android apps use (mobile/android/…/pocket/PocketException.java):
//   Bluetooth: not-configured, not-found, auth, unsupported, permission, busy, timeout,
//              disconnected, no-answer, failed
//   WiFi:      wifi-unsupported, wifi-setup, wifi-join, wifi-ap, stuck, refused, transfer,
//              cancelled
//   copy:      firmware, battery, no-server, signed-out, offline, server
struct PocketError: Error, CustomStringConvertible {
    let code: String
    let message: String

    init(_ code: String, _ message: String? = nil) {
        self.code = code
        self.message = (message ?? "").isEmpty ? code : message!
    }

    var description: String { message }

    // codeOf is the code of error, or fallback when it has none.
    static func codeOf(_ error: Error, fallback: String) -> String {
        (error as? PocketError)?.code ?? fallback
    }

    // messageOf is error's message.
    static func messageOf(_ error: Error) -> String {
        if let pocket = error as? PocketError { return pocket.message }
        return error.localizedDescription
    }
}

// Interrupt stops a thread's waits, like Java's Thread.interrupt in the Android app: a copy
// that is cancelled ends its waits for the recorder, the network and pauses at once. Each
// waiting thread looks up its own (current); WifiSync raises the one of its worker.
final class Interrupt {
    private static let key = "net.kleinhenz.knowpod.interrupt"
    private let lock = NSLock()
    private var raised = false

    // current is this thread's interrupt.
    static var current: Interrupt {
        let dictionary = Thread.current.threadDictionary
        if let existing = dictionary[key] as? Interrupt { return existing }
        let created = Interrupt()
        dictionary[key] = created
        return created
    }

    // install makes interrupt this thread's.
    static func install(_ interrupt: Interrupt) {
        Thread.current.threadDictionary[key] = interrupt
    }

    func raise() {
        lock.lock()
        raised = true
        lock.unlock()
    }

    var isRaised: Bool {
        lock.lock()
        defer { lock.unlock() }
        return raised
    }

    // clear takes the interrupt back (the clean-up after a cancel needs its waits) and says
    // whether it was raised.
    @discardableResult
    func clear() -> Bool {
        lock.lock()
        defer { lock.unlock() }
        let was = raised
        raised = false
        return was
    }

    // check throws 'cancelled' if this thread is interrupted.
    static func check() throws {
        if current.isRaised { throw PocketError("cancelled") }
    }

    // sleep pauses this thread for ms, throwing 'cancelled' when it's interrupted meanwhile.
    static func sleep(_ ms: Int64) throws {
        let end = Clock.now() + ms
        while true {
            try check()
            let left = end - Clock.now()
            if left <= 0 { return }
            Thread.sleep(forTimeInterval: Double(min(left, 100)) / 1000)
        }
    }
}

// Clock is a monotonic clock in ms.
enum Clock {
    static func now() -> Int64 {
        Int64(DispatchTime.now().uptimeNanoseconds / 1_000_000)
    }
}

// Waiter waits on a condition in short slices, so an interrupt ends the wait.
enum Waiter {
    // wait waits on condition (locked by the caller) until done says so or ms passed (then it
    // returns false), and throws 'cancelled' when the thread is interrupted.
    static func wait(_ condition: NSCondition, timeout ms: Int64, until done: () -> Bool) throws -> Bool {
        let end = Clock.now() + ms
        let interrupt = Interrupt.current
        while !done() {
            if interrupt.isRaised { throw PocketError("cancelled") }
            let left = end - Clock.now()
            if left <= 0 { return false }
            condition.wait(until: Date(timeIntervalSinceNow: Double(min(left, 100)) / 1000))
        }
        return true
    }
}

extension String {
    // matches says whether all of the string matches pattern (a regular expression).
    func matches(_ pattern: String) -> Bool {
        range(of: "^(?:" + pattern + ")$", options: .regularExpression) != nil
    }
}
