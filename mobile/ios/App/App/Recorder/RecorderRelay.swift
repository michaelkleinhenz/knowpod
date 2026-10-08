import Foundation

// RecorderLink is the Bluetooth connection to a knowpod recorder (RecorderBle on the iPhone).
protocol RecorderLink: AnyObject {
    // request sends a request and returns the recorder's response.
    func request(_ message: [String: Any], timeout: Int64) throws -> [String: Any]
    // read returns up to length bytes of recording id from offset, in order.
    func read(id: String, offset: Int64, length: Int) throws -> Data
}

struct RecorderHTTPResponse {
    let status: Int
    let body: [String: Any]
    let uploadOffset: Int64?
}

// RecorderHTTP calls the backend.
protocol RecorderHTTP {
    func call(method: String, url: String, headers: [String: String], body: Data?) throws -> RecorderHTTPResponse
}

// RecorderRelay uploads the recordings a knowpod recorder (the ESP32 gadget, esp32/) hands over
// Bluetooth while it has no Wi-Fi (docs/ble-transfer.md), with the recorder's own device token,
// as the recorder would itself (docs/device-protocol.md): the backend then files them under the
// device and deduplicates them with later Wi-Fi uploads. A port of desktop/src/recorder-relay.js
// and mobile/android/…/recorder/RecorderRelay.java. Failures that end the relay are PocketErrors
// with the codes no-token, other-server, token, recorder or http.
final class RecorderRelay {
    static let uploadChunk = 1024 * 1024
    static let readChunk = 256 * 1024
    static let openTimeout: Int64 = 180_000
    static let requestTimeout: Int64 = 15_000
    private static let maxChecksumResets = 2

    struct Result {
        var copied = 0
        var failed = 0
        var total = 0
        var errors: [String] = []
    }

    // progress hears how the relay goes: phase listing, preparing or uploading; current, total,
    // title, bytes, totalBytes.
    typealias Progress = (String, Int, Int, String, Int64, Int64) -> Void

    private let link: RecorderLink
    private let http: RecorderHTTP
    private let progress: Progress
    private let isCancelled: () -> Bool
    private var backend = ""
    private var token = ""

    init(link: RecorderLink, http: RecorderHTTP, progress: @escaping Progress, isCancelled: @escaping () -> Bool) {
        self.link = link
        self.http = http
        self.progress = progress
        self.isCancelled = isCancelled
    }

    static func complete(_ status: String) -> Bool {
        ["received", "stored", "transcribed", "summarized"].contains(status)
    }

    // originOf is the origin of url ("https://host[:port]"), or nil.
    static func originOf(_ url: String) -> String? {
        ServerConfig.originOf(URL(string: url.trimmingCharacters(in: .whitespaces)))
    }

    private static func int64(_ value: Any?) -> Int64? {
        (value as? NSNumber)?.int64Value
    }

    private func ask(_ message: [String: Any], timeout: Int64 = RecorderRelay.requestTimeout) throws -> [String: Any] {
        let response = try link.request(message, timeout: timeout)
        guard response["ok"] as? Bool == true else {
            let why = response["message"] as? String ?? response["error"] as? String ?? "failed"
            throw PocketError("recorder", "\(message["op"] as? String ?? ""): \(why)")
        }
        return response
    }

    private func readRange(id: String, offset: Int64, length: Int) throws -> Data {
        var out = Data(capacity: length)
        while out.count < length {
            let part = try link.read(id: id, offset: offset + Int64(out.count), length: min(Self.readChunk, length - out.count))
            if part.isEmpty { throw PocketError("recorder", "read: no data at \(offset + Int64(out.count))") }
            out.append(part.prefix(length - out.count))
        }
        return out
    }

    private func api(_ method: String, _ path: String, json: [String: Any]? = nil, data: Data? = nil, offset: Int64 = 0) throws -> RecorderHTTPResponse {
        var headers = ["Authorization": "Bearer " + token]
        var body: Data?
        if let json {
            headers["Content-Type"] = "application/json"
            body = try JSONSerialization.data(withJSONObject: json)
        } else if let data {
            headers["Content-Type"] = "application/offset+octet-stream"
            headers["Upload-Offset"] = String(offset)
            body = data
        }
        let response = try http.call(method: method, url: backend + path, headers: headers, body: body)
        if response.status == 401 { throw PocketError("token", "The backend refused the recorder's device token") }
        return response
    }

    private static func httpError(_ what: String, _ r: RecorderHTTPResponse) -> PocketError {
        let error = r.body["error"] as? String ?? ""
        return PocketError("http", "\(what): HTTP \(r.status)" + (error.isEmpty ? "" : " (\(error))"))
    }

    // run uploads what the recorder offers to serverURL (the app's server, an origin).
    func run(serverURL: String) throws -> Result {
        let info = try ask(["op": "info"])
        token = info["token"] as? String ?? ""
        backend = (info["backend"] as? String ?? "").replacingOccurrences(of: "/+$", with: "", options: .regularExpression)
        if token.isEmpty || backend.isEmpty { throw PocketError("no-token", "The recorder has no device token") }
        let origin = Self.originOf(backend)
        guard let origin, origin == Self.originOf(serverURL) else {
            throw PocketError("other-server", "The recorder uploads to \(origin ?? backend)")
        }

        progress("listing", 0, 0, "", 0, 0)
        let recordings = try ask(["op": "list"])["recordings"] as? [[String: Any]] ?? []
        var result = Result()
        result.total = recordings.count
        for (i, rec) in recordings.enumerated() {
            if isCancelled() { break }
            let id = rec["id"] as? String ?? ""
            let title = rec["title"] as? String ?? id
            progress("preparing", i + 1, recordings.count, title, 0, 0)
            let opened = try ask(["op": "open", "id": id], timeout: Self.openTimeout)
            let size = Self.int64(opened["size"]) ?? 0
            let sha256 = opened["sha256"] as? String ?? ""
            if let error = try upload(rec, id: id, size: size, sha256: sha256, current: i + 1, total: recordings.count, title: title) {
                if !error.isEmpty {
                    result.failed += 1
                    result.errors.append(error)
                }
            } else {
                result.copied += 1
            }
        }
        return result
    }

    // upload sends one recording and returns nil when it's done, an error message when it
    // failed for good, or "" when the relay was cancelled.
    private func upload(_ rec: [String: Any], id: String, size: Int64, sha256: String, current: Int, total: Int, title: String) throws -> String? {
        var create: [String: Any] = ["recordingId": id, "size": size, "sha256": sha256]
        if let at = rec["recordedAt"] as? String, !at.isEmpty { create["recordedAt"] = at }
        let marks = rec["highlights"] as? [Any] ?? []
        create["highlights"] = marks.compactMap { Self.int64($0) }.map { ["offsetMs": $0] }

        var r = try api("POST", "/uploads", json: create)
        if r.status == 409 { return "\(id): the backend already has a different recording with this id" }
        if r.status != 200 && r.status != 201 { throw Self.httpError("Creating the upload", r) }
        var uploadId = r.body["uploadId"] as? String ?? ""
        var status = r.body["status"] as? String ?? ""
        var offset = Self.int64(r.body["offset"]) ?? 0
        var resets = 0

        while !Self.complete(status) {
            if isCancelled() { return "" }
            if status == "failed" { return "\(id): the backend rejected the recording: \(r.body["error"] as? String ?? "")" }
            progress("uploading", current, total, title, offset, size)
            let n = Int(min(Int64(Self.uploadChunk), size - offset))
            if n <= 0 {
                // Everything was sent but not confirmed: ask where the upload stands.
                r = try api("GET", "/uploads/" + uploadId)
            } else {
                r = try api("PATCH", "/uploads/" + uploadId, data: try readRange(id: id, offset: offset, length: n), offset: offset)
            }
            switch r.status {
            case 200:
                status = r.body["status"] as? String ?? ""
                let next = Self.int64(r.body["offset"]) ?? r.uploadOffset ?? offset + Int64(max(n, 0))
                if n <= 0 && !Self.complete(status) && status != "failed" && next >= size {
                    throw Self.httpError("The backend has the whole file but did not finish it", r)
                }
                offset = next
            case 409:
                // Offset mismatch: continue from the backend's.
                guard let next = Self.int64(r.body["offset"]) ?? r.uploadOffset else { throw Self.httpError("Upload offset", r) }
                offset = next
            case 404:
                // Gone on the backend (purged): start a new upload.
                r = try api("POST", "/uploads", json: create)
                if r.status != 200 && r.status != 201 { throw Self.httpError("Creating the upload", r) }
                uploadId = r.body["uploadId"] as? String ?? ""
                status = r.body["status"] as? String ?? ""
                offset = Self.int64(r.body["offset"]) ?? 0
            case 422:
                // Checksum mismatch (the backend reset the offset to 0) or not a supported file.
                resets += 1
                if resets > Self.maxChecksumResets { return "\(id): backend: \(r.body["error"] as? String ?? "checksum mismatch")" }
                offset = 0
            default:
                throw Self.httpError("Uploading", r)
            }
        }
        _ = try ask(["op": "done", "id": id, "uploadId": uploadId, "status": status])
        progress("uploading", current, total, title, size, size)
        return nil
    }
}

// DeviceApi calls the backend's device API for RecorderRelay, with the recorder's device token:
// an ephemeral session, so no cookies of the web app go along.
final class DeviceApi: RecorderHTTP {
    private let session = URLSession(configuration: .ephemeral)

    // Outcome carries a task's answer from URLSession's queue.
    private final class Outcome: @unchecked Sendable {
        var data: Data?
        var response: URLResponse?
        var error: Error?
    }

    func call(method: String, url: String, headers: [String: String], body: Data?) throws -> RecorderHTTPResponse {
        guard let target = URL(string: url) else { throw PocketError("http", "Invalid URL \(url)") }
        var request = URLRequest(url: target, timeoutInterval: 180)
        request.httpMethod = method
        request.httpShouldHandleCookies = false
        for (name, value) in headers { request.setValue(value, forHTTPHeaderField: name) }
        request.httpBody = body
        let outcome = Outcome()
        let done = DispatchSemaphore(value: 0)
        let task = session.dataTask(with: request) { data, response, error in
            outcome.data = data
            outcome.response = response
            outcome.error = error
            done.signal()
        }
        task.resume()
        while done.wait(timeout: .now() + 0.2) == .timedOut {
            if Interrupt.current.isRaised {
                task.cancel()
                done.wait()
                throw PocketError("cancelled")
            }
        }
        if let error = outcome.error { throw PocketError("failed", error.localizedDescription) }
        guard let http = outcome.response as? HTTPURLResponse else { throw PocketError("failed", "No answer from the server") }
        let json = (try? JSONSerialization.jsonObject(with: outcome.data ?? Data())) as? [String: Any] ?? [:]
        let offset = http.value(forHTTPHeaderField: "Upload-Offset").flatMap { Int64($0.trimmingCharacters(in: .whitespaces)) }
        return RecorderHTTPResponse(status: http.statusCode, body: json, uploadOffset: offset)
    }
}
