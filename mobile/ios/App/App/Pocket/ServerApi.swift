import Foundation

// ServerApi calls the knowpod server as the signed-in user, with the web app's session cookie
// (the WKWebView's): which recordings aren't notes yet, and uploading one. The same endpoints the
// desktop and Android apps' copy uses (mobile/android/…/pocket/ServerApi.java). The calls wait
// for their answer: they run on the copy's thread. One lives as long as the app (its URLSession
// keeps it).
final class ServerApi: NSObject, URLSessionTaskDelegate {
    // HttpError is an answer of the server other than 2xx.
    struct HttpError: Error, CustomStringConvertible {
        let status: Int
        let description: String
    }

    private let cookies: (URL) -> String?
    private lazy var session: URLSession = {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.httpShouldSetCookies = false
        configuration.httpCookieAcceptPolicy = .never
        configuration.timeoutIntervalForRequest = 120
        configuration.waitsForConnectivity = false
        return URLSession(configuration: configuration, delegate: self, delegateQueue: nil)
    }()

    // cookies gives the Cookie header for a URL (nil for none).
    init(cookies: @escaping (URL) -> String?) {
        self.cookies = cookies
        super.init()
    }

    // newFiles returns which of names (recording file names) aren't notes yet.
    func newFiles(server: String, names: [String]) throws -> [String] {
        var request = try self.request(server + "/api/v1/me/pocket/device/check")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONSerialization.data(withJSONObject: ["files": names])
        let answer = try run { self.session.dataTask(with: request, completionHandler: $0) }
        return (answer["files"] as? [Any])?.compactMap { $0 as? String } ?? []
    }

    // upload sends a recording, streamed from its file (recordings can be hours long), and
    // returns the id of its note. 409: it already is one.
    func upload(server: String, file: URL, name: String) throws -> String {
        var request = try self.request(server + "/api/v1/me/pocket/device/files")
        request.setValue("audio/mpeg", forHTTPHeaderField: "Content-Type")
        let encoded = name.addingPercentEncoding(withAllowedCharacters: .alphanumerics.union(CharacterSet(charactersIn: "-_.!~*'()"))) ?? name
        request.setValue(encoded, forHTTPHeaderField: "X-Filename")
        let answer = try run { self.session.uploadTask(with: request, fromFile: file, completionHandler: $0) }
        return answer["id"] as? String ?? ""
    }

    private func request(_ text: String) throws -> URLRequest {
        guard let url = URL(string: text) else { throw URLError(.badURL) }
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.timeoutInterval = 120
        request.httpShouldHandleCookies = false
        if let cookie = cookies(url), !cookie.isEmpty { request.setValue(cookie, forHTTPHeaderField: "Cookie") }
        return request
    }

    // Outcome carries a task's answer from URLSession's queue.
    private final class Outcome: @unchecked Sendable {
        var data: Data?
        var response: URLResponse?
        var error: Error?
    }

    // run runs the task make makes and returns its JSON answer: URLError when the server
    // wasn't reachable, HttpError for an answer other than 2xx.
    private func run(_ make: (@escaping @Sendable (Data?, URLResponse?, Error?) -> Void) -> URLSessionTask) throws -> [String: Any] {
        let outcome = Outcome()
        let done = DispatchSemaphore(value: 0)
        let task = make { data, response, error in
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
        if let error = outcome.error { throw error }
        let status = (outcome.response as? HTTPURLResponse)?.statusCode ?? 0
        let body = outcome.data ?? Data()
        let json = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any] ?? [:]
        if status < 200 || status >= 300 {
            let message = json["message"] as? String ?? ""
            throw HttpError(status: status, description: message.isEmpty ? "HTTP \(status)" : message)
        }
        return json
    }

    // Redirects aren't followed: the cookie is the server's only.
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}
