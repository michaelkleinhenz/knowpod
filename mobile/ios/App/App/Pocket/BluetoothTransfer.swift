import Foundation

// BluetoothTransfer downloads recordings over the Bluetooth connection itself, as the Pocket
// app does for all but long recordings: no access point to raise and join, which takes longer
// than a short recording takes over Bluetooth (tools/pocket-wifi-probe/RESEARCH.md, run 6 and
// captures 1 and 2 of the Pocket app):
//
//   U&<date>&<timestamp> → MCU&U&<size>; the recorder then sends the file's raw bytes (MP3,
//   no header) as notifications of the audio characteristic (001120a1, 244 bytes each), and
//   MCU&OFF once all of it went out.
//
// The recorder only sends while the audio characteristic is subscribed to. The port of
// mobile/android/…/pocket/BluetoothTransfer.java.
final class BluetoothTransfer {
    // Timeouts, ms: for the answer to U, the first bytes, a pause in the middle, and MCU&OFF
    // after the last byte.
    struct Timings {
        var answer: Int64 = 10_000
        var firstByte: Int64 = 15_000
        var idle: Int64 = 10_000
        var offWait: Int64 = 5_000
    }

    // Chunks hands the recording data from Bluetooth's queue to the copy's thread.
    private final class Chunks {
        private let condition = NSCondition()
        private var items: [Data] = []

        func add(_ data: Data) {
            condition.lock()
            items.append(data)
            condition.signal()
            condition.unlock()
        }

        // poll takes the next chunk, or nil after ms.
        func poll(_ ms: Int64) -> Data? {
            condition.lock()
            defer { condition.unlock() }
            if items.isEmpty { condition.wait(until: Date(timeIntervalSinceNow: Double(ms) / 1000)) }
            return items.isEmpty ? nil : items.removeFirst()
        }
    }

    private let ble: PocketSession
    private let t: Timings

    init(ble: PocketSession, timings: Timings = Timings()) {
        self.ble = ble
        self.t = timings
    }

    // download transfers one recording into file (written to <file>.part, renamed once all of
    // it came) and returns its size. progress hears received bytes of size.
    @discardableResult
    func download(_ recording: PocketSession.Recording, to file: URL, progress: ((Int64, Int64) -> Void)?) throws -> Int64 {
        try ble.subscribeAudio()
        let fm = FileManager.default
        try fm.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        let chunks = Chunks()
        ble.receiveAudio { chunks.add($0) }
        let partial = URL(fileURLWithPath: file.path + ".part")
        var ok = false
        var since = -1
        defer {
            if !ok {
                try? fm.removeItem(at: partial)
                // A transfer that broke off may still be running: let it end before the next.
                if since >= 0 && ble.connected && !Interrupt.current.isRaised {
                    _ = try? ble.waitFor("OFF", since: since, timeout: t.offWait, accept: PocketSession.any)
                }
            }
            ble.receiveAudio(nil)
        }
        since = try ble.send("U&" + recording.date + "&" + recording.timestamp)
        guard let answer = try ble.waitFor("U", since: since, timeout: t.answer, accept: PocketSession.any) else {
            throw PocketError("no-answer", "No answer to U&" + recording.timestamp)
        }
        let trimmed = answer.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.matches("\\d+"), let size = Int64(trimmed) else {
            throw PocketError("refused", "The Pocket wouldn't send \(recording.timestamp) (MCU&U&\(answer))")
        }
        guard fm.createFile(atPath: partial.path, contents: nil), let out = try? FileHandle(forWritingTo: partial) else {
            throw PocketError("transfer", "Couldn't write " + partial.lastPathComponent)
        }
        var received: Int64 = 0
        do {
            defer { try? out.close() }
            var last = Clock.now()
            var offSeen: Int64 = 0
            while received < size {
                try Interrupt.check()
                let now = Clock.now()
                guard let chunk = chunks.poll(200) else {
                    if !ble.connected { throw PocketError("disconnected", "The recorder disconnected") }
                    // MCU&OFF ends the transfer; bytes still under way get a moment.
                    if offSeen == 0 && !ble.values("OFF", since: since).isEmpty { offSeen = now }
                    if offSeen > 0 && now - offSeen > 1_000 {
                        throw PocketError("transfer", "The Pocket stopped sending at \(received) of \(size) bytes")
                    }
                    if now - last > (received == 0 ? t.firstByte : t.idle) {
                        throw PocketError("transfer", received == 0 ? "The Pocket sent nothing"
                            : "The transfer stalled at \(received) of \(size) bytes")
                    }
                    continue
                }
                last = now
                // Anything past the size isn't the file.
                let n = Int(min(Int64(chunk.count), size - received))
                try out.write(contentsOf: chunk.prefix(n))
                received += Int64(n)
                progress?(received, size)
            }
        }
        if try ble.waitFor("OFF", since: since, timeout: t.offWait, accept: PocketSession.any) == nil {
            throw PocketError("transfer", "The Pocket did not report the end of the transfer")
        }
        try? fm.removeItem(at: file)
        do {
            try fm.moveItem(at: partial, to: file)
        } catch {
            throw PocketError("transfer", "Couldn't rename " + partial.lastPathComponent)
        }
        ok = true
        return size
    }
}
