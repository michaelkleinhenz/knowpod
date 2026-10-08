package net.kleinhenz.knowpod;

import android.net.Uri;
import android.os.Handler;
import android.os.Looper;
import android.util.Log;
import android.webkit.WebView;
import androidx.annotation.NonNull;
import androidx.webkit.JavaScriptReplyProxy;
import androidx.webkit.ScriptHandler;
import androidx.webkit.WebMessageCompat;
import androidx.webkit.WebViewCompat;
import androidx.webkit.WebViewFeature;
import java.util.Collections;
import net.kleinhenz.knowpod.pocket.PocketController;
import net.kleinhenz.knowpod.recorder.RecorderController;
import org.json.JSONException;
import org.json.JSONObject;

// AppBridge tells the web app it runs in the Android app (window.knowpodAndroid, see
// frontend/src/lib/desktop.ts), the counterpart of the desktop app's preload script
// (desktop/src/preload.js): it lets the web app show notifications and open the page of a
// clicked one, set up the Pocket recorder's Bluetooth connection and copy from the Pocket
// over its WiFi, pair the knowpod recorder and copy from it over Bluetooth, and go back to the
// setup page.
//
// Only pages of the knowpod server get it: the message channel and the script that wraps it
// are both limited to the server's origin by the WebView itself.
final class AppBridge {
    private static final String TAG = "knowpod";
    private static final String CHANNEL = "knowpodNative";

    private final MainActivity activity;
    private final WebView webView;
    private final Handler main = new Handler(Looper.getMainLooper());
    private ScriptHandler script;
    private boolean listening;
    // reply reaches the page that said hello last: for events like a clicked notification.
    private JavaScriptReplyProxy page;

    AppBridge(MainActivity activity, WebView webView) {
        this.activity = activity;
        this.webView = webView;
    }

    // supported says whether this WebView can limit the bridge to one origin (WebView 88+).
    static boolean supported() {
        return WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)
                && WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT);
    }

    // install offers the bridge to the pages of server (an origin), and only to them.
    void install(String server) {
        uninstall();
        if (server == null || !supported()) return;
        try {
            WebViewCompat.addWebMessageListener(webView, CHANNEL, Collections.singleton(server), this::onMessage);
            listening = true;
            script = WebViewCompat.addDocumentStartJavaScript(webView, shim(), Collections.singleton(server));
        } catch (IllegalArgumentException e) {
            Log.e(TAG, "bridge: " + e.getMessage());
            uninstall();
        }
    }

    private void uninstall() {
        if (script != null) {
            script.remove();
            script = null;
        }
        if (listening) {
            WebViewCompat.removeWebMessageListener(webView, CHANNEL);
            listening = false;
        }
        page = null;
    }

    // shim is the window.knowpodAndroid the web app sees: calls go over the message channel
    // as {id, method, args} and come back as {id, result}; events as {event, …}.
    private String shim() {
        return "(() => {\n"
                + "  const native = window." + CHANNEL + ";\n"
                + "  if (!native || window.knowpodAndroid) return;\n"
                + "  const pending = new Map();\n"
                + "  const openListeners = new Set();\n"
                + "  let next = 1;\n"
                + "  const post = (message) => native.postMessage(JSON.stringify(message));\n"
                + "  native.onmessage = (event) => {\n"
                + "    let m;\n"
                + "    try { m = JSON.parse(event.data); } catch { return; }\n"
                + "    if (m.event === 'open') openListeners.forEach((l) => l(m.url));\n"
                + "    else if (pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); }\n"
                + "  };\n"
                + "  const call = (method, args) => new Promise((resolve) => {\n"
                + "    const id = next++;\n"
                + "    pending.set(id, resolve);\n"
                + "    post({ id, method, args });\n"
                + "  });\n"
                + "  Object.defineProperty(window, 'knowpodAndroid', { value: Object.freeze({\n"
                + "    platform: 'android',\n"
                + "    version: " + JSONObject.quote(activity.versionName()) + ",\n"
                + "    notify: (message) => post({ method: 'notify', args: message }),\n"
                + "    pocketBluetooth: (request) => call('pocketBluetooth', request),\n"
                + "    recorderBluetooth: (request) => call('recorderBluetooth', request),\n"
                + "    showSetup: () => post({ method: 'showSetup' }),\n"
                + "    onOpen: (listener) => { openListeners.add(listener); return () => openListeners.delete(listener); },\n"
                + "  }) });\n"
                + "  post({ method: 'hello' });\n"
                + "})();\n";
    }

    private void onMessage(@NonNull WebView view, @NonNull WebMessageCompat message, @NonNull Uri sourceOrigin,
            boolean isMainFrame, @NonNull JavaScriptReplyProxy reply) {
        // Frames from elsewhere can't share the origin, but the main frame is all that needs it.
        if (!isMainFrame || message.getData() == null) return;
        JSONObject m;
        try {
            m = new JSONObject(message.getData());
        } catch (JSONException e) {
            return;
        }
        String method = m.optString("method");
        switch (method) {
            case "hello":
                page = reply;
                activity.onAppPageReady();
                break;
            case "notify": {
                JSONObject args = m.optJSONObject("args");
                if (args != null) Notifications.show(activity, args);
                break;
            }
            case "showSetup":
                activity.showSetup(null);
                break;
            case "pocketBluetooth": {
                int id = m.optInt("id");
                JSONObject request = m.optJSONObject("args");
                PocketController.get(activity).handle(activity, request == null ? new JSONObject() : request,
                        result -> main.post(() -> answer(reply, id, result)));
                break;
            }
            case "recorderBluetooth": {
                int id = m.optInt("id");
                JSONObject request = m.optJSONObject("args");
                RecorderController.get(activity).handle(activity, request == null ? new JSONObject() : request,
                        result -> main.post(() -> answer(reply, id, result)));
                break;
            }
            default:
                break;
        }
    }

    private void answer(JavaScriptReplyProxy reply, int id, JSONObject result) {
        try {
            JSONObject out = new JSONObject();
            out.put("id", id);
            out.put("result", result);
            reply.postMessage(out.toString());
        } catch (JSONException | IllegalStateException e) {
            // the page is gone
        }
    }

    // open tells the page to show path (a clicked notification); false if no page listens.
    boolean open(String url) {
        if (page == null) return false;
        try {
            JSONObject out = new JSONObject();
            out.put("event", "open");
            out.put("url", url);
            page.postMessage(out.toString());
            return true;
        } catch (JSONException | IllegalStateException e) {
            return false;
        }
    }

    // forgetPage: a new page is loading; it says hello when it's ready.
    void forgetPage() {
        page = null;
    }
}
