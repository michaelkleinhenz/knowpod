import Capacitor
import UIKit
import WebKit

// SetupPlugin is the call of the bundled setup page (www/), which asks for the knowpod server's
// address, like the Android app's (mobile/android/…/SetupPlugin.java). It answers only while
// the app's own page (capacitor://localhost) is shown, never a page the server sent.
//
// It also keeps the server's pages in the app and opens links to other sites in Safari.
@objc(SetupPlugin)
public class SetupPlugin: CAPPlugin, CAPBridgedPlugin {
    public let identifier = "SetupPlugin"
    public let jsName = "KnowpodSetup"
    public let pluginMethods: [CAPPluginMethod] = [
        CAPPluginMethod(name: "get", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "setServer", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "cancel", returnType: CAPPluginReturnPromise),
    ]

    // get returns the server set now and why loading it failed, if it did.
    @objc func get(_ call: CAPPluginCall) {
        onSetupPage(call) { controller in
            call.resolve(["current": ServerConfig.serverURL() ?? "", "error": controller.setupError])
        }
    }

    // setServer saves the server and loads it.
    @objc func setServer(_ call: CAPPluginCall) {
        onSetupPage(call) { controller in
            guard let url = ServerConfig.normalize(call.getString("url")) else {
                call.resolve(["ok": false])
                return
            }
            ServerConfig.setServerURL(url)
            call.resolve(["ok": true, "url": url])
            controller.loadServer()
        }
    }

    // cancel goes back to the server set now.
    @objc func cancel(_ call: CAPPluginCall) {
        onSetupPage(call) { controller in
            call.resolve()
            controller.loadServer()
        }
    }

    // onSetupPage runs body on the main thread if the setup page is what the app shows.
    private func onSetupPage(_ call: CAPPluginCall, _ body: @escaping @MainActor (MainViewController) -> Void) {
        Task { @MainActor [weak self] in
            guard let bridge = self?.bridge, let controller = bridge.viewController as? MainViewController,
                  let url = bridge.webView?.url, url.scheme == bridge.config.localURL.scheme, url.host == bridge.config.localURL.host
            else {
                call.reject("Only the app's setup page can change the server.")
                return
            }
            body(controller)
        }
    }

    // shouldOverrideLoad keeps the server's pages in the app, also those opened as a new
    // window, and opens links to other sites in Safari (and mailto: and tel: in their apps).
    // nil leaves the app's own pages to Capacitor.
    override public func shouldOverrideLoad(_ navigationAction: WKNavigationAction) -> NSNumber? {
        guard let url = navigationAction.request.url else { return nil }
        let mainFrame = navigationAction.targetFrame?.isMainFrame ?? true
        if ServerConfig.isServerURL(url) {
            if navigationAction.targetFrame == nil {
                bridge?.webView?.load(navigationAction.request)
                return true
            }
            return false
        }
        guard mainFrame else { return nil }
        if let local = bridge?.config.localURL, url.scheme == local.scheme, url.host == local.host { return nil }
        if let scheme = url.scheme?.lowercased(), ["http", "https", "mailto", "tel"].contains(scheme) {
            UIApplication.shared.open(url)
        }
        return true
    }
}
