import Capacitor
import UIKit
import WebKit

// knowpod for iOS: the knowpod web app in a WKWebView, like the Android app (mobile/android/)
// and the desktop app (desktop/). The backend isn't bundled; the app loads the web UI from a
// knowpod server, whose address the bundled setup page (www/) asks for on the first start.
// It shows the server's notifications while it runs, and, like the Android app, copies from a
// Pocket recorder over Bluetooth and the recorder's WiFi (Pocket/).
class MainViewController: CAPBridgeViewController {
    // current is the app's controller, for a clicked notification.
    private static weak var current: MainViewController?
    // pendingOpen is a page to open once the controller is there (a clicked notification
    // that started the app).
    private static var pendingOpen: String?

    private(set) var setupError = ""
    // pendingPath is a page to open once the server's page is loaded.
    private var pendingPath: String?
    private var appBridge: AppBridge?
    private var navigationProxy: NavigationProxy?

    override func capacitorDidLoad() {
        bridge?.registerPluginInstance(SetupPlugin())
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        guard let webView = bridge?.webView else { return }
        Self.current = self
        appBridge = AppBridge(controller: self, webView: webView)
        let proxy = NavigationProxy(inner: webView.navigationDelegate, controller: self)
        webView.navigationDelegate = proxy
        navigationProxy = proxy
        webView.allowsBackForwardNavigationGestures = true
        pendingPath = Self.pendingOpen
        Self.pendingOpen = nil
        loadServer()
    }

    // open shows a page of the server (a clicked notification): the web app navigates itself,
    // unless the app shows something else, e.g. the setup page.
    static func open(_ path: String) {
        guard path.hasPrefix("/"), !path.hasPrefix("//") else { return }
        guard let controller = current else {
            pendingOpen = path
            return
        }
        controller.show(path)
    }

    private func show(_ path: String) {
        guard let server = ServerConfig.serverURL() else { return }
        if ServerConfig.isServerURL(bridge?.webView?.url), appBridge?.open(server + path) == true { return }
        pendingPath = path
        loadServer()
    }

    var versionName: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? ""
    }

    // loadServer loads the server's web app, or the setup page if there is no server yet.
    func loadServer() {
        guard let webView = bridge?.webView else { return }
        guard let server = ServerConfig.serverURL(), let url = URL(string: server + (pendingPath ?? "")) else {
            showSetup(nil)
            return
        }
        setupError = ""
        pendingPath = nil
        appBridge?.install(server: server)
        webView.load(URLRequest(url: url))
    }

    // showSetup shows the setup page, with why the server couldn't be loaded.
    func showSetup(_ error: String?) {
        setupError = error ?? ""
        guard let bridge else { return }
        bridge.webView?.load(URLRequest(url: bridge.config.localURL))
    }

    // pageStarted: a new page is loading; it says hello when it's ready.
    func pageStarted() {
        appBridge?.forgetPage()
    }

    // loadFailed: a page couldn't be loaded. A server that can't be reached shows the setup
    // page with the error, so the address can be corrected.
    func loadFailed(_ error: Error) {
        let nsError = error as NSError
        if nsError.domain == NSURLErrorDomain && nsError.code == NSURLErrorCancelled { return }
        // 102: the load was handed elsewhere, e.g. a link opened in Safari
        if nsError.domain == "WebKitErrorDomain" && nsError.code == 102 { return }
        let url = nsError.userInfo[NSURLErrorFailingURLErrorKey] as? URL
        guard let url, ServerConfig.isServerURL(url) else { return }
        showSetup("\(error.localizedDescription) (\(url.absoluteString))")
    }
}

// NavigationProxy sits in front of Capacitor's navigation delegate to learn of failed loads and
// new pages; everything else goes to Capacitor's.
@MainActor
final class NavigationProxy: NSObject, WKNavigationDelegate {
    private nonisolated(unsafe) weak var inner: WKNavigationDelegate?
    private weak var controller: MainViewController?

    init(inner: WKNavigationDelegate?, controller: MainViewController) {
        self.inner = inner
        self.controller = controller
        super.init()
    }

    nonisolated override func responds(to aSelector: Selector!) -> Bool {
        super.responds(to: aSelector) || (inner?.responds(to: aSelector) ?? false)
    }

    nonisolated override func forwardingTarget(for aSelector: Selector!) -> Any? {
        if let inner, inner.responds(to: aSelector) { return inner }
        return super.forwardingTarget(for: aSelector)
    }

    func webView(_ webView: WKWebView, didStartProvisionalNavigation navigation: WKNavigation!) {
        inner?.webView?(webView, didStartProvisionalNavigation: navigation)
        controller?.pageStarted()
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        inner?.webView?(webView, didFailProvisionalNavigation: navigation, withError: error)
        controller?.loadFailed(error)
    }
}
