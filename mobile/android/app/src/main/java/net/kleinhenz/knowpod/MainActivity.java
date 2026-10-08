package net.kleinhenz.knowpod;

import android.Manifest;
import android.app.DownloadManager;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageInfo;
import android.content.pm.PackageManager;
import android.graphics.Bitmap;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.webkit.CookieManager;
import android.webkit.URLUtil;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebView;
import android.widget.Toast;
import androidx.activity.OnBackPressedCallback;
import androidx.activity.result.ActivityResultLauncher;
import androidx.activity.result.contract.ActivityResultContracts;
import androidx.core.content.ContextCompat;
import com.getcapacitor.Bridge;
import com.getcapacitor.BridgeActivity;
import com.getcapacitor.BridgeWebViewClient;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Consumer;
import net.kleinhenz.knowpod.recorder.RecorderController;

// knowpod for Android: the knowpod web app in a WebView, like the desktop app (desktop/) and the
// installed web app. The backend isn't bundled; the app loads the web UI from a knowpod server,
// whose address the bundled setup page (www/) asks for on the first start. Beyond the web app it
// copies recordings from a Pocket recorder over the recorder's WiFi (pocket/), which a browser
// can't: that needs Bluetooth, joining the recorder's network and a plain TCP connection.
public class MainActivity extends BridgeActivity {
    private AppBridge appBridge;
    private String setupError = "";
    // pendingPath is a page to open once the server's page is loaded (a clicked notification).
    private String pendingPath;
    private ActivityResultLauncher<String[]> permissionLauncher;
    private Consumer<Map<String, Boolean>> permissionCallback;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        registerPlugin(SetupPlugin.class);
        permissionLauncher = registerForActivityResult(new ActivityResultContracts.RequestMultiplePermissions(), result -> {
            Consumer<Map<String, Boolean>> callback = permissionCallback;
            permissionCallback = null;
            if (callback != null) callback.accept(result);
        });
        super.onCreate(savedInstanceState);
        Notifications.createChannels(this);
        if (bridge == null) return; // no WebView on this device

        WebView webView = bridge.getWebView();
        bridge.setWebViewClient(new AppWebViewClient(bridge));
        webView.setDownloadListener(this::download);
        appBridge = new AppBridge(this, webView);
        getOnBackPressedDispatcher().addCallback(this, new OnBackPressedCallback(true) {
            @Override
            public void handleOnBackPressed() {
                if (webView.canGoBack()) {
                    webView.goBack();
                } else {
                    // The app keeps running (and a Pocket copy with it); the system decides.
                    moveTaskToBack(true);
                }
            }
        });
        pendingPath = pathOf(getIntent());
        loadServer();
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        // Capacitor also calls this from onCreate, before the bridge is set up; onCreate
        // takes the launch intent's page itself.
        if (appBridge == null) return;
        String path = pathOf(intent);
        if (path != null) open(path);
    }

    // While the app is open, it looks for the paired knowpod recorder now and then, to copy what
    // it couldn't upload over Wi-Fi (recorder/RecorderController).
    @Override
    public void onResume() {
        super.onResume();
        RecorderController.get(this).resume();
    }

    @Override
    public void onPause() {
        RecorderController.get(this).pause();
        super.onPause();
    }

    private static String pathOf(Intent intent) {
        String path = intent == null ? null : intent.getStringExtra(Notifications.EXTRA_URL);
        return path != null && path.startsWith("/") && !path.startsWith("//") ? path : null;
    }

    String versionName() {
        try {
            PackageInfo info = getPackageManager().getPackageInfo(getPackageName(), 0);
            return info.versionName == null ? "" : info.versionName;
        } catch (PackageManager.NameNotFoundException e) {
            return "";
        }
    }

    String setupError() {
        return setupError;
    }

    // loadServer loads the server's web app, or the setup page if there is no server yet.
    void loadServer() {
        String server = ServerConfig.serverUrl(this);
        if (server == null) {
            showSetup(null);
            return;
        }
        setupError = "";
        appBridge.install(server);
        String path = pendingPath;
        pendingPath = null;
        bridge.getWebView().loadUrl(path == null ? server : server + path);
    }

    // showSetup shows the setup page, with why the server couldn't be loaded.
    void showSetup(String error) {
        setupError = error == null ? "" : error;
        bridge.getWebView().loadUrl(bridge.getLocalUrl());
    }

    // open shows a page of the server: the web app navigates itself, unless the WebView shows
    // something else, e.g. the setup page.
    private void open(String path) {
        String server = ServerConfig.serverUrl(this);
        if (server == null) return;
        if (ServerConfig.isServerUrl(this, bridge.getWebView().getUrl()) && appBridge.open(server + path)) return;
        pendingPath = path;
        loadServer();
    }

    // onAppPageReady: a page of the server is loaded and has the bridge. Asks once for
    // notifications (Android 13+), which the server's reminders need.
    void onAppPageReady() {
        SharedPreferences prefs = getSharedPreferences("knowpod", MODE_PRIVATE);
        if (Build.VERSION.SDK_INT >= 33 && !Notifications.allowed(this) && !prefs.getBoolean("notificationsAsked", false)) {
            prefs.edit().putBoolean("notificationsAsked", true).apply();
            requestPermissions(new String[] {Manifest.permission.POST_NOTIFICATIONS}, result -> {});
        }
    }

    // requestPermissions asks for the permissions not granted yet; callback gets each one's
    // state. On the main thread.
    public void requestPermissions(String[] permissions, Consumer<Map<String, Boolean>> callback) {
        runOnUiThread(() -> {
            List<String> missing = new ArrayList<>();
            Map<String, Boolean> state = new HashMap<>();
            for (String p : permissions) {
                boolean granted = ContextCompat.checkSelfPermission(this, p) == PackageManager.PERMISSION_GRANTED;
                state.put(p, granted);
                if (!granted) missing.add(p);
            }
            if (missing.isEmpty() || permissionCallback != null) {
                callback.accept(state);
                return;
            }
            permissionCallback = result -> {
                state.putAll(result);
                callback.accept(state);
            };
            permissionLauncher.launch(missing.toArray(new String[0]));
        });
    }

    // download saves a file the web app offers (audio, transcripts, exports) to Downloads, with
    // the session cookie for the server's own files.
    private void download(String url, String userAgent, String contentDisposition, String mimeType, long length) {
        if (!URLUtil.isHttpUrl(url) && !URLUtil.isHttpsUrl(url)) {
            Toast.makeText(this, R.string.download_unsupported, Toast.LENGTH_LONG).show();
            return;
        }
        String name = URLUtil.guessFileName(url, contentDisposition, mimeType);
        DownloadManager.Request request = new DownloadManager.Request(Uri.parse(url));
        if (ServerConfig.isServerUrl(this, url)) {
            String cookie = CookieManager.getInstance().getCookie(url);
            if (cookie != null) request.addRequestHeader("Cookie", cookie);
        }
        request.addRequestHeader("User-Agent", userAgent);
        request.setMimeType(mimeType);
        request.setTitle(name);
        request.setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED);
        request.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name);
        DownloadManager manager = (DownloadManager) getSystemService(DOWNLOAD_SERVICE);
        manager.enqueue(request);
        Toast.makeText(this, getString(R.string.download_started, name), Toast.LENGTH_SHORT).show();
    }

    // AppWebViewClient keeps the server's pages in the app and opens links to other sites in
    // the browser; a server that can't be reached shows the setup page with the error, so the
    // address can be corrected.
    private class AppWebViewClient extends BridgeWebViewClient {
        AppWebViewClient(Bridge bridge) {
            super(bridge);
        }

        @Override
        public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
            String url = request.getUrl().toString();
            if (ServerConfig.isServerUrl(MainActivity.this, url)) return false;
            if (url.startsWith(bridge.getLocalUrl())) return super.shouldOverrideUrlLoading(view, request);
            String scheme = request.getUrl().getScheme();
            if (scheme != null && scheme.matches("(?i)https?|mailto|tel")) {
                try {
                    startActivity(new Intent(Intent.ACTION_VIEW, request.getUrl()));
                } catch (ActivityNotFoundException e) {
                    // nothing opens it
                }
            }
            return true;
        }

        @Override
        public void onPageStarted(WebView view, String url, Bitmap favicon) {
            super.onPageStarted(view, url, favicon);
            appBridge.forgetPage();
        }

        @Override
        public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
            super.onReceivedError(view, request, error);
            String url = request.getUrl().toString();
            if (request.isForMainFrame() && ServerConfig.isServerUrl(MainActivity.this, url)) {
                showSetup(error.getDescription() + " (" + url + ")");
            }
        }
    }
}
