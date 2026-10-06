import Foundation

// PocketLink is the Bluetooth connection of a PocketSession: writes go to the command
// characteristic, in order, from any thread (IOSBle on the phone).
protocol PocketLink: AnyObject {
    func write(_ bytes: Data) throws

    // subscribeAudio subscribes to the recording data of a Bluetooth transfer: the raw bytes
    // of the file, in notifications that go to listener in order. A transfer only runs while
    // it's subscribed to, and the WiFi transfer switches a running Bluetooth transfer over.
    func subscribeAudio(_ listener: @escaping (Data) -> Void) throws

    func close()
}

// PocketSession talks to a Pocket recorder (heypocketai.com) over a Bluetooth connection: the
// app writes "APP&<command>" to a characteristic, and the recorder answers
// "MCU&<command>&<value>" in notifications of the same characteristic (kept in a MessageLog).
// A connection has to be unlocked with the recorder's session key first ("APP&SK&<key>",
// answered "MCU&SK&OK"). The commands used here:
//   BAT         → MCU&BAT&58              battery, percent
//   FW          → MCU&FW&1.8              firmware version
//   SPACE       → MCU&SPA&060846&061032   storage used and total, KB
//   GET&USB     → MCU&USB&1               whether the recorder is a USB drive when plugged in
//   LIST_DIRS   → MCU&DIRS&<date>… MCU&DIRS_SUM&<n>                 days with recordings
//   LIST&<date> → MCU&F&<date>&<timestamp>&<seconds>… MCU&LIST&<n>   a day's recordings
//   U&<date>&<timestamp> → MCU&U&<size>, then the file over Bluetooth (BluetoothTransfer) … MCU&OFF
// and for the WiFi transfer WIFIO, WIFI, WIFIS, WPING, U&WIFI and WIFIC (see WifiSession).
// The port of mobile/android/…/pocket/PocketSession.java.
final class PocketSession {
    // How long to wait for an answer, ms.
    static let answerTimeout: Int64 = 5_000
    // How long to wait for the end of a listing.
    static let listTimeout: Int64 = 10_000
    // How long to let late repeats of a listing's answers come in before the next listing.
    static let listSettle: Int64 = 250

    // The checks waitFor and command take.
    static let any: (String) -> Bool = { _ in true }
    static let one: (String) -> Bool = { $0.trimmingCharacters(in: .whitespaces) == "1" }
    static let pair: (String) -> Bool = { $0.contains("&") }

    private let link: PocketLink
    private let log: MessageLog
    // subscribing: one subscription at a time; sinkLock: the sink, taken by Bluetooth's queue.
    private let subscribing = NSLock()
    private let sinkLock = NSLock()
    private var audio = false
    // Where the recording data goes; nil throws it away (a transfer switched to WiFi).
    private var audioSink: ((Data) -> Void)?

    init(link: PocketLink, log: MessageLog) {
        self.link = link
        self.log = log
    }

    func mark() -> Int {
        log.mark()
    }

    var connected: Bool {
        !log.isDisconnected
    }

    // send writes "APP&<name>" and returns the mark before it.
    @discardableResult
    func send(_ name: String) throws -> Int {
        if log.isDisconnected { throw PocketError("disconnected", "The recorder disconnected") }
        let since = log.mark()
        do {
            try link.write(Data(("APP&" + name).utf8))
        } catch {
            throw PocketError(log.isDisconnected ? "disconnected" : "failed", PocketError.messageOf(error))
        }
        return since
    }

    // waitFor returns the value of the first answer to answer after since that accept takes,
    // or nil after timeout ms.
    func waitFor(_ answer: String, since: Int, timeout: Int64, accept: (String) -> Bool) throws -> String? {
        try log.waitFor(answer, since: since, timeout: timeout, accept: accept)
    }

    // command sends name and returns the value of its answer, or throws 'no-answer'.
    func command(_ name: String, _ answer: String, timeout: Int64 = PocketSession.answerTimeout,
                 accept: (String) -> Bool = PocketSession.any) throws -> String {
        guard let value = try waitFor(answer, since: send(name), timeout: timeout, accept: accept) else {
            throw PocketError("no-answer", "No answer to " + name)
        }
        return value
    }

    func values(_ answer: String, since: Int) -> [String] {
        log.values(answer, since: since)
    }

    func subscribeAudio() throws {
        subscribing.lock()
        defer { subscribing.unlock() }
        if audio { return }
        do {
            try link.subscribeAudio { [weak self] bytes in
                guard let self else { return }
                self.sinkLock.lock()
                let sink = self.audioSink
                self.sinkLock.unlock()
                sink?(bytes)
            }
        } catch let error as PocketError {
            throw error
        } catch {
            throw PocketError("failed", PocketError.messageOf(error))
        }
        audio = true
    }

    // receiveAudio sends the recording data of Bluetooth transfers to sink; nil throws it away.
    func receiveAudio(_ sink: ((Data) -> Void)?) {
        sinkLock.lock()
        audioSink = sink
        sinkLock.unlock()
    }

    func close() {
        link.close()
    }

    // optional runs a command whose answer is nice to have, returning nil without one.
    private func optional(_ name: String, _ answer: String) throws -> String? {
        do {
            return try command(name, answer)
        } catch let error as PocketError where error.code == "no-answer" {
            return nil
        }
    }

    // unlock unlocks the connection with the session key.
    func unlock(_ sessionKey: String) throws {
        if try optional("SK&" + sessionKey, "SK") != "OK" {
            throw PocketError("auth", "The recorder refused the session key")
        }
    }

    // check reads the battery, firmware, storage and USB state, as the settings page shows them.
    func check() throws -> [String: Any] {
        let battery = try Self.parseInt(command("BAT", "BAT"))
        let firmware = try optional("FW", "FW")
        let space = try optional("SPACE", "SPA")
        let usb = try optional("GET&USB", "USB")
        var info: [String: Any] = [:]
        info["battery"] = Self.orNull(battery)
        info["firmware"] = Self.orNull(firmware?.trimmingCharacters(in: .whitespacesAndNewlines))
        var storage: Any = NSNull()
        if let space {
            let parts = space.components(separatedBy: "&")
            if parts.count > 1, let used = Self.parseInt(parts[0]), let total = Self.parseInt(parts[1]) {
                storage = ["usedKB": used, "totalKB": total]
            }
        }
        info["storage"] = storage
        let usbOn: Bool? = usb == "1" ? true : (usb == "0" ? false : nil)
        info["usb"] = Self.orNull(usbOn)
        return info
    }

    // orNull is value, or null in JSON.
    static func orNull(_ value: Any?) -> Any {
        value ?? NSNull()
    }

    // parseInt reads the number text starts with.
    static func parseInt(_ text: String?) -> Int? {
        guard let text, let range = text.range(of: "^\\s*-?\\d+", options: .regularExpression) else { return nil }
        return Int(text[range].trimmingCharacters(in: .whitespacesAndNewlines))
    }

    // Recording is one recording on the recorder.
    struct Recording {
        let date: String // 2026-10-03
        let timestamp: String // 20261003142550
        let seconds: Int

        var name: String { timestamp + ".mp3" }
    }

    // Listing is the recorder's recordings, oldest first; incomplete if its listing came back
    // short, rather than the missing recordings looking copied.
    struct Listing {
        let recordings: [Recording]
        let days: Int
        let incomplete: Bool
    }

    private struct Rows {
        let rows: [String]
        let isShort: Bool
    }

    // collect sends name and returns the rows (the distinct values of answer that keep takes)
    // up to an end that counts them, and whether fewer came than it counts.
    private func collect(_ name: String, _ answer: String, _ end: String, _ keep: @escaping (String) -> Bool) throws -> Rows {
        let since = try send(name)
        let counted: (String) -> Bool = { value in
            self.rows(answer, since, keep).count >= (Self.parseInt(value) ?? 0)
        }
        let value = try waitFor(end, since: since, timeout: Self.listTimeout, accept: counted)
        let ends = values(end, since: since)
        if value == nil && ends.isEmpty { throw PocketError("no-answer", "No end of the answer to " + name) }
        try Interrupt.sleep(Self.listSettle)
        let expected = ends.compactMap { Self.parseInt($0) }.max() ?? 0
        let found = rows(answer, since, keep)
        return Rows(rows: found, isShort: value == nil || found.count < max(0, expected))
    }

    private func rows(_ answer: String, _ since: Int, _ keep: (String) -> Bool) -> [String] {
        var seen = Set<String>()
        var kept: [String] = []
        for row in values(answer, since: since) where !seen.contains(row) {
            seen.insert(row)
            if keep(row) { kept.append(row) }
        }
        return kept
    }

    // again asks once more when the answer came back short.
    private func again(_ name: String, _ answer: String, _ end: String, _ keep: @escaping (String) -> Bool) throws -> Rows {
        let first = try collect(name, answer, end, keep)
        if !first.isShort { return first }
        let second = try collect(name, answer, end, keep)
        return second.rows.count >= first.rows.count ? second : first
    }

    // listRecordings lists the recorder's recordings, oldest first. Repeats are dropped: some
    // systems deliver each notification several times, a few milliseconds apart, so a late
    // repeat of one day's end ("MCU&LIST&<n>") can come in after the next day was asked for. A
    // listing ends only at an end whose count its rows reach, and the repeats are let in
    // before the next listing is asked for. A listing that comes back short (fewer rows than
    // its end counts, or a day without any) is asked for again; if it stays short, incomplete
    // says so.
    func listRecordings() throws -> Listing {
        var incomplete = false
        var recordings: [String: Recording] = [:]
        let dirs = try again("LIST_DIRS", "DIRS", "DIRS_SUM") { $0.matches("\\d{4}-\\d{2}-\\d{2}") }
        if dirs.isShort { incomplete = true }
        for day in dirs.rows {
            let keep: (String) -> Bool = { row in
                let parts = row.components(separatedBy: "&")
                return parts[0] == day && parts.count > 1 && parts[1].matches("\\d{14}")
            }
            // A day is listed because it has recordings: none means its answer got lost.
            var list = try again("LIST&" + day, "F", "LIST", keep)
            if list.rows.isEmpty { list = try collect("LIST&" + day, "F", "LIST", keep) }
            if list.isShort || list.rows.isEmpty { incomplete = true }
            for row in list.rows {
                let parts = row.components(separatedBy: "&")
                let seconds = parts.count > 2 ? Self.parseInt(parts[2]) : nil
                recordings[parts[1]] = Recording(date: parts[0], timestamp: parts[1], seconds: seconds ?? 0)
            }
        }
        let sorted = recordings.keys.sorted().compactMap { recordings[$0] }
        return Listing(recordings: sorted, days: dirs.rows.count, incomplete: incomplete)
    }
}
