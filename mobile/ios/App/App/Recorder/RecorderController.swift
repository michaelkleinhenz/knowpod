import Foundation
import UIKit

// RecorderController answers the web app's knowpod recorder calls (window.knowpodIOS.
// recorderBluetooth, see frontend/src/lib/desktop.ts), the same requests the desktop and Android
// apps answer (desktop/src/recorder-bluetooth.js, mobile/android/…/recorder/RecorderController.java):
// {action: 'state' | 'pair' | 'pin' | 'sync' | 'cancel' | 'enable' | 'forget', name?, enabled?}.
// Where the recorder (the ESP32 gadget) has no Wi-Fi, it hands its recordings over Bluetooth and
// the iPhone uploads them with the recorder's device token (RecorderRelay, docs/ble-transfer.md).
// Pairing is asked for once from the settings page; iOS asks for the passkey itself. While the
// app is in front, it looks for the paired recorder every two minutes.
final class RecorderController {
    static let shared = RecorderController()

    private static let lookInterval: TimeInterval = 120
    private static let scanTimeout: Int64 = 15_000
    private static let pairScanTimeout: Int64 = 30_000
    private static let pairSettle: Int64 = 3_000
    private static let pairTimeout: Int64 = 120_000

    private let defaults = UserDefaults.standard
    private let lock = NSLock()
    // Guarded by lock
    private var busy = false
    private var state: [String: Any] = ["phase": ""]
    private var interrupt: Interrupt?
    // Main thread only
    private var timer: Timer?
    private var observing = false
    private var backgroundTask: UIBackgroundTaskIdentifier = .invalid

    private init() {}

    private var peripheralID: UUID? { defaults.string(forKey: "recorder.peripheral").flatMap(UUID.init(uuidString:)) }
    private var name: String { defaults.string(forKey: "recorder.name") ?? "" }
    private var paired: Bool { peripheralID != nil }
    private var enabled: Bool { defaults.object(forKey: "recorder.enabled") as? Bool ?? true }

    private func set(_ values: [String: Any]) {
        lock.lock()
        state.merge(values) { _, new in new }
        lock.unlock()
    }

    private static func ok() -> [String: Any] {
        ["ok": true]
    }

    private static func failure(_ error: String) -> [String: Any] {
        ["ok": false, "error": error]
    }

    private func stateResult() -> [String: Any] {
        lock.lock()
        var result = state
        result["busy"] = busy
        lock.unlock()
        result["ok"] = true
        result["paired"] = paired
        result["name"] = name
        result["enabled"] = enabled
        result["systemPin"] = true
        return result
    }

    // activate starts looking for the recorder while the app is in front (once a page of the
    // server is ready).
    @MainActor
    func activate() {
        if !observing {
            observing = true
            let center = NotificationCenter.default
            center.addObserver(forName: UIApplication.didBecomeActiveNotification, object: nil, queue: .main) { [weak self] _ in
                Task { @MainActor in self?.schedule() }
            }
            center.addObserver(forName: UIApplication.willResignActiveNotification, object: nil, queue: .main) { [weak self] _ in
                Task { @MainActor in self?.stopLooking() }
            }
        }
        schedule()
    }

    @MainActor
    private func stopLooking() {
        timer?.invalidate()
        timer = nil
    }

    @MainActor
    private func schedule() {
        timer?.invalidate()
        timer = nil
        guard paired, enabled, UIApplication.shared.applicationState == .active else { return }
        timer = Timer.scheduledTimer(withTimeInterval: Self.lookInterval, repeats: true) { [weak self] _ in
            _ = self?.run(quiet: true, initial: nil) { try $0.look(quiet: true) }
        }
    }

    // handle answers request; reply gets the answer, on any thread.
    func handle(_ request: [String: Any], reply: @escaping ([String: Any]) -> Void) {
        switch request["action"] as? String ?? "" {
        case "state":
            reply(stateResult())
        case "pair":
            let wanted = request["name"] as? String ?? ""
            let started = run(quiet: false, initial: ["phase": "searching", "devices": [String]()]) { try $0.pair(wanted) }
            reply(started ? Self.ok() : Self.failure("busy"))
        case "pin":
            // iOS asks for the passkey in its own dialog.
            reply(Self.failure("failed"))
        case "sync":
            if !paired {
                reply(Self.failure("not-paired"))
            } else {
                let started = run(quiet: false, initial: ["phase": "searching"]) { try $0.look(quiet: false) }
                reply(started ? Self.ok() : Self.failure("busy"))
            }
        case "cancel":
            lock.lock()
            interrupt?.raise()
            lock.unlock()
            reply(Self.ok())
        case "enable":
            if !paired {
                reply(Self.failure("not-paired"))
                return
            }
            defaults.set(request["enabled"] as? Bool == true, forKey: "recorder.enabled")
            Task { @MainActor [self] in schedule() }
            reply(Self.ok())
        case "forget":
            for key in ["recorder.peripheral", "recorder.name", "recorder.enabled"] { defaults.removeObject(forKey: key) }
            set(["phase": "", "error": "", "message": "", "devices": [String]()])
            Task { @MainActor [self] in schedule() }
            reply(Self.ok())
        default:
            reply(Self.failure("failed"))
        }
    }

    // run runs work on a thread of its own (the calls wait for the recorder), one at a time;
    // false when one runs already. initial: the state to show from the start (the last run's
    // outcome is cleared then).
    private func run(quiet: Bool, initial: [String: Any]?, _ work: @escaping (RecorderController) throws -> Void) -> Bool {
        lock.lock()
        if busy {
            lock.unlock()
            return false
        }
        busy = true
        if let initial {
            state.merge(["error": "", "message": "", "copied": 0, "failed": 0]) { _, new in new }
            state.merge(initial) { _, new in new }
        }
        let current = Interrupt()
        interrupt = current
        lock.unlock()
        let thread = Thread { [self] in
            Interrupt.install(current)
            do {
                try work(self)
            } catch {
                fail(error, quiet: quiet)
            }
            lock.lock()
            busy = false
            interrupt = nil
            lock.unlock()
            Task { @MainActor [self] in endBackground() }
        }
        thread.name = "recorder-bluetooth"
        thread.start()
        return true
    }

    private func pair(_ wanted: String) throws {
        let ble = try RecorderBle.open()
        defer { ble.close() }
        let found = try ble.scan(known: nil, timeout: Self.pairScanTimeout, settle: wanted.isEmpty ? Self.pairSettle : Self.pairScanTimeout)
        var chosen: RecorderBle.Found?
        if wanted.isEmpty {
            if found.count > 1 {
                set(["phase": "choose", "devices": found.map { $0.name.isEmpty ? $0.id.uuidString : $0.name }])
                return
            }
            chosen = found.first
        } else {
            chosen = found.first { $0.name == wanted || $0.id.uuidString == wanted }
        }
        guard let chosen else { throw PocketError("not-found", "No recorder in pairing mode was found") }
        set(["phase": "connecting"])
        try ble.connect(chosen.id)
        // The first request needs the paired link: iOS pairs now and asks for the passkey.
        set(["phase": "pin"])
        let info = try ble.request(["op": "info"], timeout: Self.pairTimeout)
        guard info["ok"] as? Bool == true else { throw PocketError("failed", info["message"] as? String ?? "The recorder did not answer") }
        let recorderName = info["name"] as? String ?? chosen.name
        defaults.set(chosen.id.uuidString, forKey: "recorder.peripheral")
        defaults.set(recorderName, forKey: "recorder.name")
        defaults.set(true, forKey: "recorder.enabled")
        Notifications.show(["title": "Paired with \(recorderName)",
                            "body": "Recordings it can’t upload over Wi-Fi now come over Bluetooth.",
                            "tag": "recorder-bluetooth"])
        Task { @MainActor [self] in schedule() }
        try relay(ble, name: recorderName, quiet: false)
    }

    private func look(quiet: Bool) throws {
        guard let known = peripheralID, enabled else { return }
        let ble = try RecorderBle.open()
        defer { ble.close() }
        if try ble.scan(known: known, timeout: Self.scanTimeout, settle: 0).isEmpty {
            throw PocketError("not-found", "The recorder wasn't found")
        }
        set(["phase": "connecting", "error": "", "message": ""])
        try ble.connect(known)
        try relay(ble, name: name, quiet: quiet)
    }

    private func relay(_ ble: RecorderBle, name: String, quiet: Bool) throws {
        guard let server = ServerConfig.serverURL() else { throw PocketError("no-server", "No server set up") }
        Task { @MainActor [self] in beginBackground() }
        let interrupt = Interrupt.current
        let result = try RecorderRelay(link: ble, http: DeviceApi(), progress: { [self] phase, current, total, title, bytes, totalBytes in
            set(["phase": phase, "current": current, "total": total, "title": title, "bytes": bytes, "totalBytes": totalBytes])
        }, isCancelled: { interrupt.isRaised }).run(serverURL: server)
        let message = result.errors.joined(separator: "; ")
        set(["phase": "done", "copied": result.copied, "failed": result.failed, "error": "", "message": message])
        if result.copied > 0 {
            let body = result.copied == 1 ? "1 recording was" : "\(result.copied) recordings were"
            Notifications.show(["title": "Copied from \(name)",
                                "body": "\(body) uploaded over Bluetooth and will be transcribed and summarized.",
                                "url": "/", "tag": "recorder-bluetooth"])
        } else if result.failed > 0 && !quiet {
            Notifications.show(["title": "Couldn’t copy from \(name)", "body": message, "url": "/", "tag": "recorder-bluetooth"])
        }
    }

    private func fail(_ error: Error, quiet: Bool) {
        let code = PocketError.codeOf(error, fallback: "failed")
        NSLog("knowpod recorder bluetooth: %@: %@", code, PocketError.messageOf(error))
        let silent = code == "cancelled" || (quiet && code == "not-found")
        set(["phase": silent ? "" : "failed", "error": code, "message": PocketError.messageOf(error)])
    }

    // beginBackground keeps the copy going for a while when knowpod goes to the background.
    @MainActor
    private func beginBackground() {
        if backgroundTask != .invalid { return }
        backgroundTask = UIApplication.shared.beginBackgroundTask(withName: "recorder-bluetooth") { [weak self] in
            self?.endBackground()
        }
    }

    @MainActor
    private func endBackground() {
        if backgroundTask == .invalid { return }
        UIApplication.shared.endBackgroundTask(backgroundTask)
        backgroundTask = .invalid
    }
}
