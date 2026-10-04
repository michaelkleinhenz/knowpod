package net.kleinhenz.knowpod.pocket;

import android.Manifest;
import android.content.Context;
import android.os.Build;
import android.util.Log;
import android.webkit.CookieManager;
import java.io.File;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import net.kleinhenz.knowpod.MainActivity;
import net.kleinhenz.knowpod.Notifications;
import net.kleinhenz.knowpod.R;
import net.kleinhenz.knowpod.ServerConfig;
import org.json.JSONException;
import org.json.JSONObject;

// PocketController answers the web app's Pocket calls (window.knowpodAndroid.pocketBluetooth,
// see frontend/src/lib/desktop.ts), the same requests the desktop app answers
// (desktop/src/main.js: knowpod:pocket-bluetooth): {action: 'settings' | 'save' | 'check' |
// 'state' | 'wifi-sync' | 'wifi-cancel', address?, sessionKey?}. The phone copies over the
// Pocket's WiFi only; there is no USB drive to switch on.
public final class PocketController {
    private static final String TAG = "knowpod";
    private static PocketController instance;

    // Reply gets the answer to a call, from any thread.
    public interface Reply {
        void send(JSONObject result);
    }

    private final Context context;
    private final PocketSettings settings;
    private final ExecutorService worker = Executors.newSingleThreadExecutor();
    private final WifiSync sync;
    private volatile boolean checking;

    public static synchronized PocketController get(Context context) {
        if (instance == null) instance = new PocketController(context.getApplicationContext());
        return instance;
    }

    private PocketController(Context context) {
        this.context = context;
        this.settings = new PocketSettings(context);
        this.sync = new WifiSync(new Env());
    }

    private boolean busy() {
        return checking || sync.running();
    }

    public void handle(MainActivity activity, JSONObject request, Reply reply) {
        String action = request.optString("action");
        switch (action) {
            case "settings":
                reply.send(settingsResult());
                break;
            case "save": {
                String address = request.has("address") ? request.optString("address") : null;
                String key = request.has("sessionKey") ? request.optString("sessionKey") : null;
                String error = settings.save(address, key);
                reply.send(error == null ? settingsResult() : failure(error, null));
                break;
            }
            case "check":
                check(activity, reply);
                break;
            case "state":
                reply.send(state());
                break;
            case "wifi-sync":
                startSync(activity, reply);
                break;
            case "wifi-cancel":
                sync.cancel();
                reply.send(ok());
                break;
            case "usb-on":
                reply.send(failure("unsupported", "No USB drive on the phone"));
                break;
            case "sync":
            case "eject":
                reply.send(ok()); // nothing is plugged in
                break;
            default:
                reply.send(failure("failed", "Unknown action " + action));
        }
    }

    private static JSONObject ok() {
        return put(new JSONObject(), "ok", true);
    }

    private static JSONObject failure(String error, String message) {
        JSONObject r = put(new JSONObject(), "ok", false);
        put(r, "error", error);
        if (message != null) put(r, "message", message);
        return r;
    }

    private static JSONObject put(JSONObject o, String key, Object value) {
        try {
            o.put(key, value == null ? JSONObject.NULL : value);
        } catch (JSONException e) {
            throw new IllegalStateException(e);
        }
        return o;
    }

    // settingsResult is what the settings page shows: never the key itself.
    private JSONObject settingsResult() {
        JSONObject r = ok();
        put(r, "address", settings.address());
        put(r, "sessionKeySet", !settings.sessionKey().isEmpty());
        put(r, "busy", busy());
        put(r, "usbSupported", false);
        return r;
    }

    private JSONObject state() {
        JSONObject r = ok();
        put(r, "configured", settings.configured());
        put(r, "busy", busy());
        // Copying by USB is the desktop app's: the dialog shows only the WiFi copy.
        put(r, "usbSupported", false);
        put(r, "connected", false);
        put(r, "wifiSupported", AndroidWifi.supported());
        put(r, "wifi", new JSONObject(sync.state()));
        return r;
    }

    // bluetoothPermissions are the permissions finding and talking to the recorder needs.
    private static List<String> bluetoothPermissions() {
        List<String> list = new ArrayList<>();
        if (Build.VERSION.SDK_INT >= 31) {
            list.add(Manifest.permission.BLUETOOTH_SCAN);
            list.add(Manifest.permission.BLUETOOTH_CONNECT);
        } else {
            list.add(Manifest.permission.ACCESS_FINE_LOCATION);
        }
        return list;
    }

    // withPermissions asks for the Bluetooth permissions (and the optional ones), then runs
    // granted, or answers 'permission'.
    private void withPermissions(MainActivity activity, List<String> optional, Reply reply, Runnable granted) {
        List<String> all = new ArrayList<>(bluetoothPermissions());
        all.addAll(optional);
        activity.requestPermissions(all.toArray(new String[0]), result -> {
            for (String p : bluetoothPermissions()) {
                if (!Boolean.TRUE.equals(result.get(p))) {
                    reply.send(failure("permission", "Bluetooth permission not granted"));
                    return;
                }
            }
            granted.run();
        });
    }

    private void check(MainActivity activity, Reply reply) {
        if (!settings.configured()) {
            reply.send(failure("not-configured", null));
            return;
        }
        if (busy()) {
            reply.send(failure("busy", null));
            return;
        }
        withPermissions(activity, new ArrayList<>(), reply, () -> worker.execute(() -> {
            if (busy()) {
                reply.send(failure("busy", null));
                return;
            }
            checking = true;
            PocketSession session = null;
            try {
                session = openSession();
                JSONObject r = new JSONObject(session.check());
                put(r, "ok", true);
                reply.send(r);
            } catch (PocketException e) {
                reply.send(failure(e.code, e.getMessage()));
            } catch (Exception e) {
                reply.send(failure("failed", String.valueOf(e.getMessage())));
            } finally {
                if (session != null) session.close();
                checking = false;
            }
        }));
    }

    private void startSync(MainActivity activity, Reply reply) {
        if (!AndroidWifi.supported()) {
            reply.send(failure("wifi-unsupported", null));
            return;
        }
        if (!settings.configured()) {
            reply.send(failure("not-configured", null));
            return;
        }
        if (busy() && !sync.running()) {
            reply.send(failure("busy", null));
            return;
        }
        // Optional: the location permission lets Android tell the network's BSSID, so it joins
        // the recorder's WiFi again without asking (AndroidWifi); notifications report the end.
        List<String> optional = new ArrayList<>();
        if (Build.VERSION.SDK_INT >= 31) optional.add(Manifest.permission.ACCESS_FINE_LOCATION);
        if (Build.VERSION.SDK_INT >= 33) optional.add(Manifest.permission.POST_NOTIFICATIONS);
        withPermissions(activity, optional, reply, () -> {
            if (sync.running()) {
                reply.send(ok());
                return;
            }
            if (busy()) {
                reply.send(failure("busy", null));
                return;
            }
            try {
                PocketSyncService.start(context);
            } catch (RuntimeException e) {
                // Without the service the copy still runs while the app is open.
                Log.w(TAG, "pocket wifi: no foreground service: " + e);
            }
            sync.start();
            reply.send(ok());
        });
    }

    private PocketSession openSession() throws PocketException, InterruptedException {
        PocketSession session = AndroidBle.connect(context, settings.address());
        try {
            session.unlock(settings.sessionKey());
        } catch (PocketException | InterruptedException | RuntimeException e) {
            session.close();
            throw e;
        }
        return session;
    }

    // Env is the phone's side of the copy.
    private final class Env implements WifiSync.Env {
        @Override
        public String serverUrl() {
            return ServerConfig.serverUrl(context);
        }

        @Override
        public PocketSession openSession() throws PocketException, InterruptedException {
            return PocketController.this.openSession();
        }

        @Override
        public HostWifi wifi() {
            return new AndroidWifi(context, settings);
        }

        @Override
        public ServerApi api() {
            return new ServerApi(url -> CookieManager.getInstance().getCookie(url));
        }

        @Override
        public Set<String> remembered(String server) {
            return settings.remembered(server);
        }

        @Override
        public void remember(String server, String name) {
            settings.remember(server, name);
        }

        @Override
        public File tempDir() {
            return new File(context.getCacheDir(), "pocket-wifi");
        }

        @Override
        public void log(String text) {
            Log.i(TAG, "pocket wifi: " + text);
        }

        @Override
        public void changed() {
            PocketSyncService.update(context, sync.state());
        }

        @Override
        public void finished(String lastId) {
            PocketSyncService.stop(context);
            notifyFinished(sync.state(), lastId);
        }
    }

    // notifyFinished tells how the copy ended, like the desktop app.
    private void notifyFinished(Map<String, Object> state, String lastId) {
        int copied = (Integer) state.get("copied");
        String error = String.valueOf(state.get("error"));
        if ("done".equals(state.get("phase"))) {
            if (copied == 0) return;
            Notifications.show(context, Notifications.CHANNEL_POCKET, context.getString(R.string.pocket_copied_title),
                    context.getResources().getQuantityString(R.plurals.pocket_copied_body, copied, copied),
                    copied == 1 && !lastId.isEmpty() ? "/conversations/" + lastId : "/", "pocket-sync");
        } else if (!"cancelled".equals(error)) {
            String body = (copied > 0 ? context.getResources().getQuantityString(R.plurals.pocket_copied_some, copied, copied) + " " : "")
                    + state.get("message");
            Notifications.show(context, Notifications.CHANNEL_POCKET, context.getString(R.string.pocket_failed_title), body, "/", "pocket-sync");
        }
    }
}
