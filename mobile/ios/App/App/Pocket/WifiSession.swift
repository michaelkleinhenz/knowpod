import Foundation

// WifiSession transfers recordings from a Pocket recorder over its WiFi access point, about
// 1 MB/s instead of Bluetooth's tens of KB/s. Decoded on firmware 1.8 / WiFi firmware V9
// (tools/pocket-wifi-probe/RESEARCH.md has the details); the port of
// mobile/android/…/pocket/WifiSession.java:
//
//   1. Over Bluetooth: WIFIO raises the access point, WIFI gives its name and password, and
//      WIFIS goes 3 (starting), 2 (waiting for a client), 1 (a client has joined). The network
//      is hidden, WPA2-PSK; the recorder is 192.168.200.1 and hands out 192.168.200.2.
//   2. Per file: connect to 192.168.200.1:8475 and send nothing; request the file as a
//      Bluetooth transfer (U&<date>&<timestamp> → MCU&U&<size>), and 0.3 s later switch it to
//      WiFi (U&WIFI → MCU&U&WIFI). The socket then carries the MP3 file, exactly <size> bytes,
//      and a fixed 10-byte end marker; MCU&OFF comes over Bluetooth. Close the connection.
//   3. The recorder serves two connections per access point session; then 8475 stops
//      listening until the access point is restarted (WIFIC, WIFIO). U&WIFI must never be
//      sent without a connection open: the recorder hangs until it reports MCU&SHUT.
final class WifiSession {
    static let host = "192.168.200.1"
    static let port = 8475
    static let filesPerSession = 2

    // Timings, ms. settle: how long a new connection must stay open before a file is requested
    // on it (the recorder sometimes accepts a connection and resets it right away);
    // reconnectPause: the pause between closing one connection and opening the next (the
    // recorder refuses for a few seconds after a close).
    struct Timings {
        var join: Int64 = 90_000
        var restartPause: Int64 = 2_000
        var switchDelay: Int64 = 300
        var heartbeat: Int64 = 5_000
        var poll: Int64 = 1_000
        var settle: Int64 = 500
        var reconnectPause: Int64 = 2_000
        var offWait: Int64 = 10_000
        var connectWait: Int64 = 15_000
        var closeWait: Int32 = 2_000
        var receive = Transfer.Timeouts()
    }

    // Result is a transferred recording.
    struct Result {
        let size: Int64
        let markerOk: Bool
    }

    private let ble: PocketSession
    private let wifi: HostWifi
    private let host: String
    private let port: Int
    private let log: (String) -> Void
    private let onRestart: () -> Void
    private let t: Timings
    private let timers = DispatchQueue(label: "net.kleinhenz.knowpod.pocket-wifi-timer")
    private let lock = NSLock()

    private var ssid: String?
    private var raised = false
    private var prepared = false
    private var connections = 0
    private var heartbeat: DispatchSourceTimer?
    private var transfer: Transfer?
    private var cancelled = false
    private var closedAt: Int64 = 0 // when the last transfer connection was closed

    init(ble: PocketSession, wifi: HostWifi, host: String = WifiSession.host, port: Int = WifiSession.port,
         log: @escaping (String) -> Void, onRestart: @escaping () -> Void, timings: Timings = Timings()) {
        self.ble = ble
        self.wifi = wifi
        self.host = host
        self.port = port
        self.log = log
        self.onRestart = onRestart
        self.t = timings
    }

    private var isCancelled: Bool {
        lock.lock()
        defer { lock.unlock() }
        return cancelled
    }

    private func check() throws {
        if isCancelled { throw PocketError("cancelled") }
    }

    // repeating sends name every ms over Bluetooth, its answer waited for by nobody
    // (heartbeat, polling).
    private func repeating(_ name: String, every ms: Int64) -> DispatchSourceTimer {
        let timer = DispatchSource.makeTimerSource(queue: timers)
        timer.schedule(deadline: .now() + .milliseconds(Int(ms)), repeating: .milliseconds(Int(ms)))
        timer.setEventHandler { [ble] in
            // the next step finds out when it fails
            _ = try? ble.send(name)
        }
        timer.resume()
        return timer
    }

    // raise starts the access point and joins it: WIFIO, WIFI for the credentials, then join
    // while WIFIS is polled until it reports a client (1) — the Pocket app's order.
    private func raise() throws {
        let since = ble.mark()
        raised = true
        let started = Clock.now()
        do {
            _ = try ble.command("WIFIO", "WIFIO")
        } catch let error as PocketError where error.code == "no-answer" {
            // No answer: the recorder hangs (e.g. after a switch it couldn't serve). Every later
            // attempt would fail the same way, so the copy stops here.
            throw PocketError("stuck", "The Pocket stopped answering WiFi commands")
        }
        if ssid == nil {
            let credentials = try ble.command("WIFI", "WIFI", timeout: 5_000, accept: PocketSession.pair)
            let amp = credentials.firstIndex(of: "&")!
            let name = String(credentials[..<amp])
            try wifi.prepare(ssid: name, password: String(credentials[credentials.index(after: amp)...]))
            ssid = name
            prepared = true
        }
        try check()
        let poll = repeating("WIFIS", every: t.poll)
        defer { poll.cancel() }
        let name = ssid!
        if try !wifi.join(ssid: name, deadline: started + t.join) { throw PocketError("wifi-join", "Couldn't join " + name) }
        try check()
        let left = max(1_000, started + t.join - Clock.now())
        if try ble.waitFor("WIFIS", since: since, timeout: left, accept: PocketSession.one) == nil {
            throw PocketError("wifi-ap", "The Pocket never reported this phone on its WiFi")
        }
        connections = 0
        log("On " + name)
    }

    func start() throws {
        try wifi.setup()
        try ble.subscribeAudio()
        // The Pocket app keeps a heartbeat going while the access point is up.
        heartbeat = repeating("WPING", every: t.heartbeat)
        try raise()
    }

    // download transfers one recording into file. progress hears received bytes of size.
    func download(_ recording: PocketSession.Recording, to file: URL, progress: ((Int64, Int64) -> Void)?) throws -> Result {
        try check()
        if connections >= Self.filesPerSession {
            onRestart()
            _ = try? ble.command("WIFIC", "WIFIC") // restarting anyway
            raised = false
            wifi.leave()
            try Interrupt.sleep(t.restartPause)
            try check()
            try raise()
        }
        let pause = closedAt + t.reconnectPause - Clock.now()
        if pause > 0 { try Interrupt.sleep(pause) }
        // The connection must be open BEFORE the switch (U&WIFI).
        let current: Transfer
        do {
            current = try Transfer.open(wifi: wifi, host: host, port: port, wait: t.connectWait)
        } catch {
            connections = Self.filesPerSession // not listening: a fresh access point may help
            throw error
        }
        lock.lock()
        transfer = current
        lock.unlock()
        connections += 1
        defer {
            current.close(t.closeWait)
            closedAt = Clock.now()
            lock.lock()
            if transfer === current { transfer = nil }
            lock.unlock()
        }
        do {
            try check()
            // An accepted connection the recorder resets shows up within moments; asking for the
            // file on it would start a transfer nobody can receive.
            try Interrupt.sleep(t.settle)
            if !current.alive() {
                throw PocketError("transfer", "The Pocket dropped the connection (\(current.lostReason))")
            }
            let answer = try ble.command("U&" + recording.date + "&" + recording.timestamp, "U", timeout: 10_000, accept: PocketSession.any)
            let trimmed = answer.trimmingCharacters(in: .whitespacesAndNewlines)
            guard trimmed.matches("\\d+"), let size = Int64(trimmed) else {
                throw PocketError("refused", "The Pocket wouldn't send \(recording.timestamp) (MCU&U&\(answer))")
            }
            try Interrupt.sleep(t.switchDelay)
            // Never switch without an open connection: the recorder would hang (MCU&SHUT).
            if !current.alive() {
                throw PocketError("transfer", "The connection was lost before the switch (\(current.lostReason))")
            }
            let since = try ble.send("U&WIFI")
            let markerOk = try current.receive(size: size, to: file, timeouts: t.receive, progress: progress)
            if try ble.waitFor("OFF", since: since, timeout: t.offWait, accept: PocketSession.any) == nil {
                log("The Pocket did not report the end of the transfer")
            }
            return Result(size: size, markerOk: markerOk)
        } catch {
            // The recorder's state is unknown now: the next file starts on a fresh access point.
            connections = Self.filesPerSession
            if isCancelled { throw PocketError("cancelled") }
            throw error
        }
    }

    // cancel stops a transfer that runs now; download() then throws 'cancelled'.
    func cancel() {
        lock.lock()
        cancelled = true
        let current = transfer
        lock.unlock()
        current?.abort()
    }

    // close lowers the access point and puts this device's WiFi back. Never throws.
    func close() {
        heartbeat?.cancel()
        heartbeat = nil
        if raised {
            raised = false
            _ = try? ble.command("WIFIC", "WIFIC") // lowered or gone anyway
        }
        if prepared {
            prepared = false
            wifi.restore()
        }
    }
}
