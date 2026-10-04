import Foundation
import WebKit

// AppBridge tells the web app it runs in the iOS app (window.knowpodIOS, see
// frontend/src/lib/desktop.ts), like the Android app's AppBridge.java: it lets the web app show
// notifications and open the page of a clicked one, and go back to the setup page.
//
// Only pages of the knowpod server get it: the script that offers it runs in the main frame of
// the server's origin only, and messages from anywhere else are ignored.
@MainActor
final class AppBridge: NSObject, WKScriptMessageHandler {
    private static let channel = "knowpodNative"

    private weak var controller: MainViewController?
    private weak var webView: WKWebView?
    private var script: WKUserScript?
    private var server: String?
    // pageReady: the page loaded now said hello, so it listens for events like a clicked
    // notification.
    private var pageReady = false

    init(controller: MainViewController, webView: WKWebView) {
        self.controller = controller
        self.webView = webView
        super.init()
        // The content controller keeps its handler, and the bridge lives as long as the app.
        webView.configuration.userContentController.add(self, name: Self.channel)
    }

    // install offers the bridge to the pages of server (an origin), and only to them.
    func install(server: String) {
        guard let content = webView?.configuration.userContentController else { return }
        self.server = server
        // There is no removing one script: take all out and put the others back.
        let others = content.userScripts.filter { $0 !== script }
        content.removeAllUserScripts()
        for other in others { content.addUserScript(other) }
        let added = WKUserScript(source: shim(server: server), injectionTime: .atDocumentStart, forMainFrameOnly: true)
        content.addUserScript(added)
        script = added
        pageReady = false
    }

    // shim is the window.knowpodIOS the web app sees; events come back through
    // window.__knowpodIOSEvent.
    private func shim(server: String) -> String {
        """
        (() => {
          const native = window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.\(Self.channel);
          if (!native || window.knowpodIOS || window.location.origin !== \(jsString(server))) return;
          const openListeners = new Set();
          const post = (message) => native.postMessage(JSON.stringify(message));
          Object.defineProperty(window, '__knowpodIOSEvent', { value: (m) => {
            if (m && m.event === 'open') openListeners.forEach((l) => l(m.url));
          } });
          Object.defineProperty(window, 'knowpodIOS', { value: Object.freeze({
            platform: 'ios',
            version: \(jsString(controller?.versionName ?? "")),
            notify: (message) => post({ method: 'notify', args: message }),
            showSetup: () => post({ method: 'showSetup' }),
            onOpen: (listener) => { openListeners.add(listener); return () => openListeners.delete(listener); },
          }) });
          post({ method: 'hello' });
        })();
        """
    }

    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        // Frames from elsewhere can't share the origin, but the main frame is all that needs it.
        guard message.frameInfo.isMainFrame, let server, Self.origin(of: message.frameInfo.securityOrigin) == server,
              let text = message.body as? String, let data = text.data(using: .utf8),
              let m = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { return }
        switch m["method"] as? String ?? "" {
        case "hello":
            pageReady = true
            Notifications.askOnce()
        case "notify":
            if let args = m["args"] as? [String: Any] { Notifications.show(args) }
        case "showSetup":
            controller?.showSetup(nil)
        default:
            break
        }
    }

    // open tells the page to show url (a clicked notification); false if no page listens.
    func open(_ url: String) -> Bool {
        guard pageReady, let webView, ServerConfig.isServerURL(webView.url) else { return false }
        webView.evaluateJavaScript("window.__knowpodIOSEvent && window.__knowpodIOSEvent({ event: 'open', url: \(jsString(url)) })", completionHandler: nil)
        return true
    }

    // forgetPage: a new page is loading; it says hello when it's ready.
    func forgetPage() {
        pageReady = false
    }

    private static func origin(of origin: WKSecurityOrigin) -> String? {
        let host = origin.host.contains(":") ? "[" + origin.host + "]" : origin.host
        return ServerConfig.normalize(origin.protocol + "://" + host + (origin.port == 0 ? "" : ":\(origin.port)"))
    }

    private func jsString(_ text: String) -> String {
        guard let data = try? JSONSerialization.data(withJSONObject: text, options: .fragmentsAllowed),
              let quoted = String(data: data, encoding: .utf8)
        else { return "\"\"" }
        return quoted
    }
}
