import Foundation
import Security

// PocketSettings keeps the recorder's Bluetooth address and its session key, and the files
// already copied, like the Android app (mobile/android/…/pocket/PocketSettings.java). The key is
// a secret (its first 8 characters are also the recorder's WiFi password), so it's kept in the
// keychain, on this device only, and never handed to a page. It also keeps the device iOS knows
// the recorder as (see IOSBle), since iOS doesn't tell Bluetooth addresses.
final class PocketSettings {
    private static let prefix = "pocket."
    private static let keychainService = "net.kleinhenz.knowpod.pocket"
    private static let keychainAccount = "sessionKey"
    // How many copied files are remembered per server.
    private static let maxRemembered = 20_000

    private let defaults = UserDefaults.standard
    private let lock = NSLock()

    // normalizeAddress makes "aa-bb-cc-dd-ee-ff" or "aabbccddeeff" into "AA:BB:CC:DD:EE:FF", or
    // returns nil if it isn't a Bluetooth address.
    static func normalizeAddress(_ input: String?) -> String? {
        let text = (input ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let hexDigit = "[0-9a-fA-F]"
        guard text.matches("\(hexDigit){2}([:-]?\(hexDigit){2}){5}") else { return nil }
        let hex = Array(text.uppercased().filter { $0.isHexDigit })
        return stride(from: 0, to: 12, by: 2).map { String(hex[$0..<$0 + 2]) }.joined(separator: ":")
    }

    // A session key is 16 characters; "&" would end the command it's sent in.
    static func validSessionKey(_ key: String) -> Bool {
        key.matches("[\\x21-\\x25\\x27-\\x7e]{8,64}")
    }

    var address: String {
        defaults.string(forKey: Self.prefix + "address") ?? ""
    }

    var sessionKey: String {
        var query = Self.keyQuery
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess, let data = item as? Data else { return "" }
        return String(data: data, encoding: .utf8) ?? ""
    }

    var configured: Bool {
        !address.isEmpty && !sessionKey.isEmpty
    }

    private static var keyQuery: [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: keychainService,
            kSecAttrAccount as String: keychainAccount,
        ]
    }

    // save changes the settings: address "" and sessionKey "" remove them, nil keeps them.
    // Returns an error code (invalid-address, invalid-key, failed) or nil.
    func save(address: String?, sessionKey: String?) -> String? {
        var normalized: String?
        if let address {
            normalized = address.isEmpty ? "" : Self.normalizeAddress(address)
            if normalized == nil { return "invalid-address" }
        }
        var key: String?
        if let sessionKey {
            key = sessionKey.trimmingCharacters(in: .whitespacesAndNewlines)
            if let key, !key.isEmpty, !Self.validSessionKey(key) { return "invalid-key" }
        }
        if let key {
            SecItemDelete(Self.keyQuery as CFDictionary)
            if !key.isEmpty {
                var item = Self.keyQuery
                item[kSecValueData as String] = Data(key.utf8)
                item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
                if SecItemAdd(item as CFDictionary, nil) != errSecSuccess { return "failed" }
            }
        }
        if let normalized {
            if normalized != self.address { setPeripheral(nil) }
            defaults.set(normalized, forKey: Self.prefix + "address")
        }
        return nil
    }

    // peripheral is the device iOS knows the recorder at the saved address as, once a
    // connection to it was unlocked.
    var peripheral: UUID? {
        defaults.string(forKey: Self.prefix + "peripheral").flatMap { UUID(uuidString: $0) }
    }

    func setPeripheral(_ id: UUID?) {
        defaults.set(id?.uuidString, forKey: Self.prefix + "peripheral")
    }

    // The files already copied to a server are remembered, so they aren't asked about again
    // and a note deleted for good isn't copied again either.
    private static func rememberedKey(_ server: String) -> String {
        prefix + "copied:" + server
    }

    func remembered(server: String) -> [String] {
        lock.lock()
        defer { lock.unlock() }
        return defaults.stringArray(forKey: Self.rememberedKey(server)) ?? []
    }

    func remember(server: String, name: String) {
        lock.lock()
        defer { lock.unlock() }
        var names = (defaults.stringArray(forKey: Self.rememberedKey(server)) ?? []).filter { $0 != name }
        names.append(name)
        if names.count > Self.maxRemembered { names.removeFirst(names.count - Self.maxRemembered) }
        defaults.set(names, forKey: Self.rememberedKey(server))
    }
}
