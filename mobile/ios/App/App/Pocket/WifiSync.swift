import Foundation

// WifiSyncEnv is what the copy needs from the app.
protocol WifiSyncEnv: AnyObject {
    // serverURL is the knowpod server, or nil.
    func serverURL() -> String?

    // openSession connects to the recorder over Bluetooth and unlocks it.
    func openSession() throws -> PocketSession

    // wifi is this device's WiFi, or nil if it can't join the recorder's network.
    func wifi() -> HostWifi?

    func api() -> ServerApi

    // remembered are the files copied to server before (not asked about again, so a note
    // deleted for good isn't copied again either); remember adds one.
    func remembered(server: String) -> Set<String>

    func remember(server: String, name: String)

    func tempDir() -> URL

    func log(_ text: String)

    // changed: state() changed.
    func changed()

    // finished: the copy ended (state() has how); lastId is the note of the last recording
    // copied.
    func finished(lastId: String)
}

// WifiSync copies new recordings from a Pocket recorder, as the Pocket app does: short ones
// over Bluetooth, long ones over the recorder's WiFi. The port of
// mobile/android/…/pocket/WifiSync.java. The order:
//
//   1. Bluetooth: connect, check firmware (1.8) and battery, list the recordings.
//   2. Ask the server which of them aren't notes yet.
//   3. Download those into a temporary folder: the short ones over Bluetooth
//      (BluetoothTransfer), then the long ones over the recorder's WiFi (WifiSession): raise
//      its access point, join it, download, lower it. When the iPhone can't join, the long
//      ones come over Bluetooth too.
//   4. Upload them like the desktop app does; the server puts them into the folder "Pocket AI".
//
// state() is what the Pocket Sync dialog shows (frontend/src/components/PocketUsbSync.tsx):
// phase is '' (never run), connecting, listing, checking, wifi-starting, downloading (file
// current of total, bytes of totalBytes at rate bytes/s, via bluetooth or wifi),
// wifi-restarting, reconnecting, uploading (current of total), done (copied, failed; found
// recordings on the recorder, incomplete if its listing came back short) or failed (error,
// message).
final class WifiSync {
    // Bytes per second of a recording (32 kbps MP3), to estimate sizes before the transfer.
    static let bytesPerSecond: Int64 = 4_000
    // Recordings up to this size come over Bluetooth (ten minutes of recording); longer ones
    // over WiFi (about 1 MB/s, after 15 to 60 s for raising the access point and joining it).
    static let bluetoothMaxBytes: Int64 = 2_400_000
    // The failures of raising the access point and joining it after which the long recordings
    // come over Bluetooth instead.
    private static let wifiFallback: Set<String> = ["wifi-unsupported", "wifi-off", "wifi-setup", "wifi-join", "wifi-ap"]
    // After the transfer, how long the server may take to be reachable.
    private static let reconnectTimeout: Int64 = 90_000
    private static let reconnectRetry: Int64 = 3_000

    private unowned let env: WifiSyncEnv
    private let lock = NSLock()
    private var running = false
    private var cancelled = false
    private var interrupt: Interrupt?
    private var session: WifiSession?

    private var phase = ""
    private var current = 0
    private var total = 0
    private var bytes: Int64 = 0
    private var totalBytes: Int64 = 0
    private var rate: Double = 0
    private var via = ""
    private var copied = 0
    private var failed = 0
    private var found = 0
    private var incomplete = false
    private var error = ""
    private var message = ""
    // Why the last recording that failed to transfer failed (download).
    private var lastFailure: PocketError?

    init(env: WifiSyncEnv) {
        self.env = env
    }

    var isRunning: Bool {
        lock.lock()
        defer { lock.unlock() }
        return running
    }

    private var isCancelled: Bool {
        lock.lock()
        defer { lock.unlock() }
        return cancelled
    }

    func state() -> [String: Any] {
        lock.lock()
        defer { lock.unlock() }
        return [
            "running": running,
            "cancelling": running && cancelled,
            "phase": phase,
            "current": current,
            "total": total,
            "bytes": bytes,
            "totalBytes": totalBytes,
            "rate": rate,
            "via": via,
            "copied": copied,
            "failed": failed,
            "found": found,
            "incomplete": incomplete,
            "error": error,
            "message": message,
        ]
    }

    // start runs a copy on a thread of its own unless one runs already.
    @discardableResult
    func start() -> Bool {
        lock.lock()
        if running {
            lock.unlock()
            return false
        }
        running = true
        cancelled = false
        phase = "connecting"
        current = 0
        total = 0
        copied = 0
        failed = 0
        found = 0
        bytes = 0
        totalBytes = 0
        rate = 0
        via = ""
        incomplete = false
        error = ""
        message = ""
        let interrupt = Interrupt()
        self.interrupt = interrupt
        lock.unlock()
        env.changed()
        let worker = Thread { [self] in
            Interrupt.install(interrupt)
            run()
        }
        worker.name = "pocket-wifi-sync"
        worker.start()
        return true
    }

    // cancel stops the copy that runs; the WiFi and the recorder are put back first.
    func cancel() {
        lock.lock()
        if !running {
            lock.unlock()
            return
        }
        cancelled = true
        let worker = interrupt
        let current = session
        lock.unlock()
        current?.cancel()
        // Ends a wait for the network, an answer or a pause.
        worker?.raise()
        env.changed()
    }

    private func change(_ update: () -> Void) {
        lock.lock()
        update()
        lock.unlock()
        env.changed()
    }

    private func check() throws {
        if isCancelled { throw PocketError("cancelled") }
    }

    // upload sends one file to the server, waiting for it to be reachable. Returns the note's
    // id (or "" if it already was one).
    private func upload(server: String, file: URL, name: String) throws -> String {
        let deadline = Clock.now() + Self.reconnectTimeout
        while true {
            do {
                return try env.api().upload(server: server, file: file, name: name)
            } catch let error as ServerApi.HttpError {
                if error.status == 409 { return "" } // already a note
                if error.status == 401 { throw PocketError("signed-out", error.description) }
                throw PocketError("server", error.description)
            } catch let error as PocketError {
                throw error
            } catch {
                // Not reachable yet.
                if Clock.now() >= deadline { throw PocketError("offline", error.localizedDescription) }
                try Interrupt.sleep(Self.reconnectRetry)
            }
        }
    }

    private func run() {
        let tempDir = env.tempDir()
        var ble: PocketSession?
        var copiedNow = 0
        var failedNow = 0
        var lastId = ""
        lastFailure = nil
        defer {
            // Always: lower the access point, put the WiFi back, end the Bluetooth session, and
            // don't leave recordings lying around in the temporary folder. A cancel may have
            // interrupted this thread; the clean-up needs its waits.
            Interrupt.current.clear()
            closeSession()
            ble?.close()
            try? FileManager.default.removeItem(at: tempDir)
            lock.lock()
            running = false
            lock.unlock()
            env.changed()
            env.finished(lastId: lastId)
        }
        do {
            guard let server = env.serverURL(), !server.isEmpty else { throw PocketError("no-server") }

            let opened = try env.openSession()
            ble = opened
            try check()
            let firmware = try opened.command("FW", "FW").trimmingCharacters(in: .whitespacesAndNewlines)
            if !firmware.hasPrefix("1.8") { throw PocketError("firmware", firmware) }
            if let battery = try PocketSession.parseInt(opened.command("BAT", "BAT")), battery < 10 {
                throw PocketError("battery", String(battery))
            }

            change { phase = "listing" }
            let known = env.remembered(server: server)
            let listing = try opened.listRecordings()
            let files = listing.recordings.filter { !known.contains($0.name) }
            let listed = listing.recordings.count
            change {
                found = listed
                incomplete = listing.incomplete
            }
            env.log("\(listed) recordings on \(listing.days) days\(listing.incomplete ? " (listing incomplete)" : ""), "
                + "\(listed - files.count) copied before: " + listing.recordings.map { $0.name }.joined(separator: " "))
            try check()

            change { phase = "checking" }
            var wanted = Set<String>()
            if !files.isEmpty {
                do {
                    try wanted.formUnion(env.api().newFiles(server: server, names: files.map { $0.name }))
                } catch let error as ServerApi.HttpError {
                    if error.status == 401 { throw PocketError("signed-out", error.description) }
                    throw PocketError("server", error.description)
                } catch let error as PocketError {
                    throw error
                } catch {
                    throw PocketError("offline", error.localizedDescription)
                }
            }
            let todo = files.filter { wanted.contains($0.name) }
            for r in files where !wanted.contains(r.name) { env.remember(server: server, name: r.name) }
            env.log("\(files.count - todo.count) already notes, \(todo.count) new")
            if todo.isEmpty {
                change {
                    phase = "done"
                    copied = 0
                    failed = 0
                }
                return
            }
            try check()

            try FileManager.default.createDirectory(at: tempDir, withIntermediateDirectories: true)
            let wifi = env.wifi()
            let overWifi = todo.filter { wifi != nil && Int64($0.seconds) * Self.bytesPerSecond > Self.bluetoothMaxBytes }
            let overBluetooth = todo.filter { !(wifi != nil && Int64($0.seconds) * Self.bytesPerSecond > Self.bluetoothMaxBytes) }
            env.log("\(overBluetooth.count) over Bluetooth, \(overWifi.count) over WiFi")
            change {
                current = 0
                total = todo.count
            }

            var downloaded: [PocketSession.Recording] = []
            var number = 0
            let bluetooth = BluetoothTransfer(ble: opened)
            for f in overBluetooth {
                try check()
                number += 1
                if try download(f, index: number, via: "bluetooth", tempDir: tempDir, transfer: { r, file, progress in
                    _ = try bluetooth.download(r, to: file, progress: progress)
                }) {
                    downloaded.append(f)
                } else {
                    failedNow += 1
                }
            }

            var wifiSession: WifiSession?
            if let wifi, !overWifi.isEmpty {
                try check()
                change { phase = "wifi-starting" }
                let started = WifiSession(ble: opened, wifi: wifi, log: { [env] in env.log($0) },
                                          onRestart: { [weak self] in self?.change { self?.phase = "wifi-restarting" } })
                lock.lock()
                session = started
                lock.unlock()
                do {
                    try started.start()
                    wifiSession = started
                } catch let error as PocketError where Self.wifiFallback.contains(error.code) && !isCancelled {
                    // As the Pocket app does: the long ones come over Bluetooth, slower.
                    env.log("WiFi: \(error.message); copying over Bluetooth instead")
                    closeSession()
                }
            }
            for f in overWifi {
                try check()
                number += 1
                let ok: Bool
                if let ws = wifiSession {
                    ok = try download(f, index: number, via: "wifi", tempDir: tempDir) { r, file, progress in
                        _ = try ws.download(r, to: file, progress: progress)
                    }
                } else {
                    ok = try download(f, index: number, via: "bluetooth", tempDir: tempDir) { r, file, progress in
                        _ = try bluetooth.download(r, to: file, progress: progress)
                    }
                }
                if ok {
                    downloaded.append(f)
                } else {
                    failedNow += 1
                }
            }

            change { phase = "reconnecting" }
            closeSession()
            opened.close()
            ble = nil

            for (i, f) in downloaded.enumerated() {
                try check()
                change {
                    phase = "uploading"
                    current = i + 1
                    total = downloaded.count
                }
                let file = tempDir.appendingPathComponent(f.name)
                let id = try upload(server: server, file: file, name: f.name)
                if !id.isEmpty { lastId = id }
                env.remember(server: server, name: f.name)
                copiedNow += 1
                let copiedSoFar = copiedNow
                change { copied = copiedSoFar }
                try? FileManager.default.removeItem(at: file)
            }
            let c = copiedNow
            let fl = failedNow
            let last = lastFailure
            change {
                phase = "done"
                copied = c
                failed = fl
                error = fl > 0 ? (last?.code ?? "failed") : ""
                message = fl > 0 ? (last?.message ?? "") : ""
            }
        } catch {
            let code = isCancelled ? "cancelled" : PocketError.codeOf(error, fallback: "failed")
            let text = PocketError.messageOf(error)
            let c = copiedNow
            change {
                phase = "failed"
                self.error = code
                message = text
                copied = c
            }
            if code != "cancelled" { env.log("failed: \(text)") }
        }
    }

    // download transfers recording f, number index of the copy, into tempDir via how, showing
    // the progress. Returns false if this one recording failed (lastFailure says why); throws
    // when nothing more can be transferred.
    private func download(_ f: PocketSession.Recording, index: Int, via how: String, tempDir: URL,
                          transfer: (PocketSession.Recording, URL, @escaping (Int64, Int64) -> Void) throws -> Void) throws -> Bool {
        let file = tempDir.appendingPathComponent(f.name)
        let started = Clock.now()
        change {
            phase = "downloading"
            via = how
            current = index
            bytes = 0
            totalBytes = Int64(f.seconds) * Self.bytesPerSecond
            rate = 0
        }
        do {
            try transfer(f, file) { [weak self] received, size in
                let seconds = Double(Clock.now() - started) / 1000
                self?.change {
                    self?.phase = "downloading"
                    self?.bytes = received
                    self?.totalBytes = size
                    self?.rate = seconds > 0.5 ? Double(received) / seconds : 0
                }
            }
            return true
        } catch let error as PocketError {
            if isCancelled || error.code == "cancelled" { throw PocketError("cancelled") }
            // The Bluetooth connection is gone, or the recorder stopped answering: nothing more
            // can be transferred.
            if ["disconnected", "timeout", "stuck"].contains(error.code) { throw error }
            lastFailure = error
            env.log("\(f.name) (\(how)): \(error.message)")
            return false
        } catch {
            if isCancelled { throw PocketError("cancelled") }
            lastFailure = PocketError("transfer", error.localizedDescription)
            env.log("\(f.name) (\(how)): \(error)")
            return false
        }
    }

    private func closeSession() {
        lock.lock()
        let current = session
        session = nil
        lock.unlock()
        current?.close()
    }
}
