import Foundation

// ServerConfig keeps the knowpod server the app loads: the one entered on the setup page, else
// the one baked in at build time (KNOWPOD_DEFAULT_SERVER_URL, see Info.plist). Like the Android
// app's (mobile/android/…/ServerConfig.java).
enum ServerConfig {
    private static let key = "serverUrl"

    // normalize makes an origin of what the user typed ("knowpod.example.com" →
    // "https://knowpod.example.com"), or returns nil if it isn't an http(s) address.
    static func normalize(_ input: String?) -> String? {
        var text = (input ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        if text.isEmpty { return nil }
        if text.range(of: "^[a-z][a-z0-9+.-]*://", options: [.regularExpression, .caseInsensitive]) == nil {
            text = "https://" + text
        }
        guard let url = URL(string: text) else { return nil }
        let scheme = (url.scheme ?? "").lowercased()
        guard scheme == "https" || scheme == "http", var host = url.host, !host.isEmpty else { return nil }
        guard host.range(of: "^[A-Za-z0-9.\\-\\[\\]:]+$", options: .regularExpression) != nil else { return nil }
        if host.contains(":") && !host.hasPrefix("[") { host = "[" + host + "]" }
        let port = url.port
        let defaultPort = port == nil || (scheme == "https" && port == 443) || (scheme == "http" && port == 80)
        return scheme + "://" + host.lowercased() + (defaultPort ? "" : ":\(port!)")
    }

    // serverURL is the server to load, or nil.
    static func serverURL() -> String? {
        if let saved = normalize(UserDefaults.standard.string(forKey: key)) { return saved }
        return normalize(Bundle.main.object(forInfoDictionaryKey: "KnowpodDefaultServerURL") as? String)
    }

    static func setServerURL(_ url: String) {
        UserDefaults.standard.set(url, forKey: key)
    }

    // originOf is the origin of url ("https://host[:port]"), or nil.
    static func originOf(_ url: URL?) -> String? {
        guard let url, let scheme = url.scheme?.lowercased(), scheme == "https" || scheme == "http" else { return nil }
        return normalize(url.absoluteString)
    }

    // isServerURL says whether url is a page of the server itself.
    static func isServerURL(_ url: URL?) -> Bool {
        guard let server = serverURL() else { return false }
        return originOf(url) == server
    }
}
