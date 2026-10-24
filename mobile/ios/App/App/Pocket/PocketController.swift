import Foundation
import UIKit
import WebKit

// PocketController answers the web app's Pocket calls (window.knowpodIOS.pocketBluetooth, see
// frontend/src/lib/desktop.ts), the same requests the desktop and Android apps answer
// (mobile/android/…/pocket/PocketController.java): {action: 'settings' | 'save' | 'check' |
// 'activate' | 'key' | 'state' | 'wifi-sync' | 'wifi-cancel', address?, sessionKey?}. The iPhone copies over
// Bluetooth and the Pocket's WiFi (WifiSync); there is no USB drive to switch on.
final class PocketController: WifiSyncEnv {
    static let shared = PocketController()

    private let settings = PocketSettings()
    private let worker = DispatchQueue(label: "net.kleinhenz.knowpod.pocket")
    private let lock = NSLock()
    private var checking = false
    private var sync: WifiSync!
    private let server = ServerApi(cookies: { PocketController.cookieHeader(for: $0) })
    // While a copy runs: the time iOS gives it when knowpod goes to the background (main only).
    private var backgroundTask: UIBackgroundTaskIdentifier = .invalid

    private init() {
        sync = WifiSync(env: self)
    }

    private var busy: Bool {
        lock.lock()
        defer { lock.unlock() }
        return checking || sync.isRunning
    }

    // handle answers request; reply gets the answer, on any thread.
    func handle(_ request: [String: Any], reply: @escaping ([String: Any]) -> Void) {
        let action = request["action"] as? String ?? ""
        switch action {
        case "settings":
            reply(settingsResult())
        case "save":
            let address = request["address"] as? String
            let key = request["sessionKey"] as? String
            // The keychain may take a moment: not on the main thread.
            worker.async { [self] in
                if let error = settings.save(address: address, sessionKey: key) {
                    reply(Self.failure(error))
                } else {
                    reply(settingsResult())
                }
            }
        case "key":
            // The session key, for the settings page to show when asked (to back it up). The
            // keychain may take a moment: not on the main thread.
            worker.async { [self] in
                let key = settings.sessionKey
                if key.isEmpty {
                    reply(Self.failure("not-configured"))
                } else {
                    var result = Self.ok()
                    result["sessionKey"] = key
                    reply(result)
                }
            }
        case "check":
            check(reply)
        case "activate":
            activate(request["address"] as? String ?? "", reply)
        case "state":
            reply(state())
        case "wifi-sync":
            startSync(reply)
        case "wifi-cancel":
            sync.cancel()
            reply(Self.ok())
        case "usb-on":
            reply(Self.failure("unsupported", "No USB drive on the phone"))
        case "sync", "eject":
            reply(Self.ok()) // nothing is plugged in
        default:
            reply(Self.failure("failed", "Unknown action " + action))
        }
    }

    private static func ok() -> [String: Any] {
        ["ok": true]
    }

    private static func failure(_ error: String, _ message: String? = nil) -> [String: Any] {
        var result: [String: Any] = ["ok": false, "error": error]
        if let message { result["message"] = message }
        return result
    }

    // settingsResult is what the settings page shows: never the key itself ("key" hands it over
    // when asked).
    private func settingsResult() -> [String: Any] {
        var result = Self.ok()
        result["address"] = settings.address
        result["sessionKeySet"] = !settings.sessionKey.isEmpty
        result["keySupported"] = true
        result["busy"] = busy
        result["usbSupported"] = false
        // The recorder can be activated here; iOS hides Bluetooth addresses, so its address has
        // to be given.
        result["activateSupported"] = true
        result["activateFindsAddress"] = false
        return result
    }

    private func state() -> [String: Any] {
        var result = Self.ok()
        result["configured"] = settings.configured
        result["busy"] = busy
        // Copying by USB is the desktop app's: the dialog shows only the WiFi copy.
        result["usbSupported"] = false
        result["connected"] = false
        // The copy works on every iPhone: without the Pocket's WiFi, all recordings come over
        // Bluetooth.
        result["wifiSupported"] = true
        result["wifi"] = sync.state()
        return result
    }

    private func check(_ reply: @escaping ([String: Any]) -> Void) {
        if !settings.configured {
            reply(Self.failure("not-configured"))
            return
        }
        lock.lock()
        if checking || sync.isRunning {
            lock.unlock()
            reply(Self.failure("busy"))
            return
        }
        checking = true
        lock.unlock()
        // A thread of its own: the call waits for the recorder.
        let thread = Thread { [self] in
            defer {
                lock.lock()
                checking = false
                lock.unlock()
            }
            var session: PocketSession?
            do {
                let opened = try openSession()
                session = opened
                var result = try opened.check()
                result["ok"] = true
                reply(result)
            } catch {
                reply(Self.failure(PocketError.codeOf(error, fallback: "failed"), PocketError.messageOf(error)))
            }
            session?.close()
        }
        thread.name = "pocket-check"
        thread.start()
    }

    // After activating, the recorder drops the connection; reconnect after this long, this often.
    private static let reconnectDelay: Int64 = 2_000
    private static let reconnectAttempts = 3

    // generateSessionKey makes a key for activating a recorder: 16 letters and digits, the form
    // the Pocket app uses.
    static func generateSessionKey() -> String {
        let alphabet = Array("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
        var generator = SystemRandomNumberGenerator()
        return String((0..<16).map { _ in alphabet.randomElement(using: &generator)! })
    }

    // activate gives a factory-reset recorder at address a new session key, as the desktop app
    // does (desktop/src/pocket-bluetooth.js: activate): the address and key are saved before the
    // key is sent; a refused key restores the settings before. Then it reconnects with the key,
    // sets the clock and checks.
    private func activate(_ input: String, _ reply: @escaping ([String: Any]) -> Void) {
        guard let address = PocketSettings.normalizeAddress(input) else {
            reply(Self.failure("invalid-address"))
            return
        }
        lock.lock()
        if checking || sync.isRunning {
            lock.unlock()
            reply(Self.failure("busy"))
            return
        }
        checking = true
        lock.unlock()
        let thread = Thread { [self] in
            defer {
                lock.lock()
                checking = false
                lock.unlock()
            }
            let previousAddress = settings.address
            let previousKey = settings.sessionKey
            let restore = { _ = self.settings.save(address: previousAddress, sessionKey: previousKey) }
            do {
                let key = Self.generateSessionKey()
                if let error = settings.save(address: address, sessionKey: key) {
                    reply(Self.failure(error))
                    return
                }
                let answer: String?
                do {
                    let opened = try IOSBle.connect(address: address, known: nil)
                    defer { opened.session.close() }
                    answer = try opened.session.activate(key)
                } catch {
                    restore()
                    throw error
                }
                if answer == "ERR" {
                    restore()
                    reply(Self.failure("activated-elsewhere"))
                    return
                }
                // No answer: the recorder may have taken the key all the same; it stays saved.
                if answer != "OK" {
                    reply(Self.failure("activate-no-answer"))
                    return
                }
                var info: [String: Any]?
                for _ in 1...Self.reconnectAttempts {
                    try Interrupt.sleep(Self.reconnectDelay)
                    do {
                        let session = try openSession()
                        defer { session.close() }
                        info = try session.activated()
                        break
                    } catch let error as PocketError {
                        NSLog("pocket bluetooth: check after activating: \(error.code) \(error.message)")
                        if error.code == "auth" { break }
                    }
                }
                var result = settingsResult()
                for (name, value) in info ?? [:] { result[name] = value }
                result["busy"] = false
                result["verified"] = info != nil
                reply(result)
            } catch {
                reply(Self.failure(PocketError.codeOf(error, fallback: "failed"), PocketError.messageOf(error)))
            }
        }
        thread.name = "pocket-activate"
        thread.start()
    }

    private func startSync(_ reply: @escaping ([String: Any]) -> Void) {
        if !settings.configured {
            reply(Self.failure("not-configured"))
            return
        }
        if sync.isRunning {
            reply(Self.ok())
            return
        }
        if busy {
            reply(Self.failure("busy"))
            return
        }
        Task { @MainActor [self] in
            beginBackground()
            sync.start()
            reply(Self.ok())
        }
    }

    // beginBackground keeps the copy going for a while when knowpod goes to the background,
    // and the screen on while it runs.
    @MainActor
    private func beginBackground() {
        UIApplication.shared.isIdleTimerDisabled = true
        if backgroundTask != .invalid { return }
        backgroundTask = UIApplication.shared.beginBackgroundTask(withName: "pocket-sync") { [weak self] in
            self?.endBackground()
        }
    }

    @MainActor
    private func endBackground() {
        UIApplication.shared.isIdleTimerDisabled = false
        if backgroundTask == .invalid { return }
        UIApplication.shared.endBackgroundTask(backgroundTask)
        backgroundTask = .invalid
    }

    func openSession() throws -> PocketSession {
        let opened = try IOSBle.connect(address: settings.address, known: settings.peripheral)
        do {
            try opened.session.unlock(settings.sessionKey)
        } catch {
            opened.session.close()
            throw error
        }
        // The session key proved it's the recorder at the address: found at once next time.
        settings.setPeripheral(opened.peripheral)
        return opened.session
    }

    // cookieHeader is the web app's Cookie header for url, from the WKWebView's cookies. Waits
    // for the main thread: never call it there.
    static func cookieHeader(for url: URL) -> String? {
        let done = DispatchSemaphore(value: 0)
        let box = CookieBox()
        Task { @MainActor in
            WKWebsiteDataStore.default().httpCookieStore.getAllCookies { cookies in
                box.cookies = cookies
                done.signal()
            }
        }
        if done.wait(timeout: .now() + 10) == .timedOut { return nil }
        let host = (url.host ?? "").lowercased()
        let path = url.path.isEmpty ? "/" : url.path
        let now = Date()
        let matching = box.cookies.filter { cookie in
            let domain = cookie.domain.lowercased()
            let domainOk = domain.hasPrefix(".") ? host == String(domain.dropFirst()) || host.hasSuffix(domain) : host == domain
            let pathOk = cookie.path.isEmpty || path.hasPrefix(cookie.path)
            let secureOk = !cookie.isSecure || url.scheme?.lowercased() == "https"
            let fresh = cookie.expiresDate.map { $0 > now } ?? true
            return domainOk && pathOk && secureOk && fresh
        }
        return HTTPCookie.requestHeaderFields(with: matching)["Cookie"]
    }

    private final class CookieBox: @unchecked Sendable {
        var cookies: [HTTPCookie] = []
    }

    // MARK: WifiSyncEnv, the iPhone's side of the copy

    func serverURL() -> String? {
        ServerConfig.serverURL()
    }

    // wifi is nil once iOS refused to let this version join networks: the long recordings then
    // come over Bluetooth without trying (and waiting for) the WiFi first.
    func wifi() -> HostWifi? {
        if settings.wifiUnavailable {
            log("WiFi: this app can't join networks (no Hotspot Configuration capability); copying over Bluetooth")
            return nil
        }
        return IOSWifi(log: { [weak self] in self?.log($0) }, unavailable: { [settings] in settings.setWifiUnavailable() })
    }

    func api() -> ServerApi {
        server
    }

    func remembered(server: String) -> Set<String> {
        Set(settings.remembered(server: server))
    }

    func remember(server: String, name: String) {
        settings.remember(server: server, name: name)
    }

    func tempDir() -> URL {
        FileManager.default.temporaryDirectory.appendingPathComponent("pocket-wifi", isDirectory: true)
    }

    func log(_ text: String) {
        NSLog("knowpod pocket: %@", text)
    }

    func changed() {
        // The dialog asks for the state itself.
    }

    func finished(lastId: String) {
        let state = sync.state()
        Task { @MainActor [self] in
            endBackground()
        }
        notifyFinished(state, lastId: lastId)
    }

    // notifyFinished tells how the copy ended, like the Android app.
    private func notifyFinished(_ state: [String: Any], lastId: String) {
        let copied = state["copied"] as? Int ?? 0
        let error = state["error"] as? String ?? ""
        if state["phase"] as? String == "done" {
            if copied == 0 { return }
            Notifications.show([
                "title": Texts.copiedTitle,
                "body": Texts.copiedBody(copied),
                "url": copied == 1 && !lastId.isEmpty ? "/conversations/" + lastId : "/",
                "tag": "pocket-sync",
            ])
        } else if error != "cancelled" {
            let message = state["message"] as? String ?? ""
            Notifications.show([
                "title": Texts.failedTitle,
                "body": (copied > 0 ? Texts.copiedSome(copied) + " " : "") + message,
                "url": "/",
                "tag": "pocket-sync",
            ])
        }
    }

    // Texts are the notifications' texts, in English or German like the Android app's.
    private enum Texts {
        static var german: Bool {
            Locale.preferredLanguages.first?.lowercased().hasPrefix("de") ?? false
        }

        static var copiedTitle: String {
            german ? "Vom Pocket kopiert" : "Copied from Pocket"
        }

        static func copiedBody(_ n: Int) -> String {
            if german {
                return n == 1 ? "1 neue Aufnahme liegt im Ordner „Pocket AI“ und wird transkribiert und zusammengefasst."
                    : "\(n) neue Aufnahmen liegen im Ordner „Pocket AI“ und werden transkribiert und zusammengefasst."
            }
            return n == 1 ? "1 new recording is in the folder “Pocket AI” and will be transcribed and summarized."
                : "\(n) new recordings are in the folder “Pocket AI” and will be transcribed and summarized."
        }

        static func copiedSome(_ n: Int) -> String {
            german ? "\(n) kopiert." : "\(n) copied."
        }

        static var failedTitle: String {
            german ? "Kopieren vom Pocket fehlgeschlagen" : "Couldn’t copy from Pocket"
        }
    }
}
