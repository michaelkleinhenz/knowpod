import Darwin
import Foundation
import NetworkExtension

// IOSWifi joins the recorder's network with a hotspot configuration (NEHotspotConfiguration,
// which needs the Hotspot Configuration capability, see App.entitlements): iOS asks the user
// once to allow joining it, then switches the WiFi over to it, without internet; connections
// to the recorder go out over it since it's the recorder's subnet. Removing the configuration
// puts the iPhone back on its usual WiFi. The counterpart of mobile/android/…/AndroidWifi.java.
//
// The configuration stays while the recorder restarts its access point after every two
// recordings, so iOS joins it again without asking. Without the capability (an app re-signed
// without it) joining fails, and WifiSync copies the long recordings over Bluetooth too.
final class IOSWifi: HostWifi {
    private var ssid: String?
    private var password = ""
    private var configured = false
    private let log: (String) -> Void

    init(log: @escaping (String) -> Void) {
        self.log = log
    }

    func setup() throws {
        // iOS doesn't tell apps whether WiFi is on; joining finds out.
    }

    func prepare(ssid: String, password: String) throws {
        self.ssid = ssid
        self.password = password
    }

    // Box carries the outcome of a call that answers on another queue.
    private final class Box: @unchecked Sendable {
        var error: Error?
    }

    func join(ssid: String, deadline: Int64) throws -> Bool {
        let configuration = NEHotspotConfiguration(ssid: ssid, passphrase: password, isWEP: false)
        configuration.hidden = true
        // Kept while the app goes to the background meanwhile; restore() removes it.
        configuration.joinOnce = false
        let done = DispatchSemaphore(value: 0)
        let box = Box()
        NEHotspotConfigurationManager.shared.apply(configuration) { error in
            box.error = error
            done.signal()
        }
        configured = true
        while done.wait(timeout: .now() + 0.1) == .timedOut {
            try Interrupt.check()
            if Clock.now() >= deadline { return false }
        }
        if let error = box.error as NSError? {
            let already = error.domain == NEHotspotConfigurationErrorDomain
                && error.code == NEHotspotConfigurationError.alreadyAssociated.rawValue
            if !already {
                log("WiFi: couldn't join \(ssid): \(error.localizedDescription) (\(error.domain) \(error.code))")
                return false
            }
        }
        // iOS reports the configuration applied before the iPhone has an address on the network.
        while Clock.now() < deadline {
            if Self.onRecorderSubnet() { return true }
            try Interrupt.sleep(250)
        }
        return Self.onRecorderSubnet()
    }

    // onRecorderSubnet says whether the iPhone has an address on the recorder's network.
    static func onRecorderSubnet() -> Bool {
        let prefix = String(WifiSession.host[...WifiSession.host.lastIndex(of: ".")!])
        var list: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&list) == 0, let first = list else { return false }
        defer { freeifaddrs(list) }
        var cursor: UnsafeMutablePointer<ifaddrs>? = first
        while let entry = cursor {
            if let address = entry.pointee.ifa_addr, address.pointee.sa_family == UInt8(AF_INET) {
                var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
                if getnameinfo(address, socklen_t(address.pointee.sa_len), &host, socklen_t(host.count), nil, 0, NI_NUMERICHOST) == 0,
                   String(cString: host).hasPrefix(prefix) {
                    return true
                }
            }
            cursor = entry.pointee.ifa_next
        }
        return false
    }

    func leave() {
        // The configuration stays: the restarted access point is joined again without asking.
    }

    func restore() {
        guard configured, let ssid else { return }
        configured = false
        NEHotspotConfigurationManager.shared.removeConfiguration(forSSID: ssid)
    }

    func connect(host: String, port: Int, timeout: Int32) throws -> Int32 {
        try Transfer.connectSocket(host: host, port: port, timeout: timeout)
    }
}
