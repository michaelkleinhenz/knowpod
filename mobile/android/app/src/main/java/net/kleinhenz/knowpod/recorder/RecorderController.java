package net.kleinhenz.knowpod.recorder;

import android.Manifest;
import android.content.Context;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.os.Build;
import android.util.Log;
import androidx.core.content.ContextCompat;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.TimeUnit;
import net.kleinhenz.knowpod.MainActivity;
import net.kleinhenz.knowpod.Notifications;
import net.kleinhenz.knowpod.R;
import net.kleinhenz.knowpod.ServerConfig;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

// RecorderController answers the web app's knowpod recorder calls (window.knowpodAndroid.
// recorderBluetooth, see frontend/src/lib/desktop.ts), the same requests the desktop app answers
// (desktop/src/recorder-bluetooth.js): {action: 'state' | 'pair' | 'pin' | 'sync' | 'cancel' |
// 'enable' | 'forget', name?, enabled?}. Where the recorder (the ESP32 gadget) has no Wi-Fi, it
// hands its recordings over Bluetooth and the phone uploads them with the recorder's device token
// (RecorderRelay, docs/ble-transfer.md). Pairing is asked for once from the settings page; Android
// asks for the passkey itself. While the app is open, it looks for the paired recorder every two
// minutes.
public final class RecorderController {
    private static final String TAG = "knowpod";
    private static final String PREFS = "recorder";
    private static RecorderController instance;

    // How often to look for the recorder while the app is open, and for how long.
    private static final long LOOK_INTERVAL = 2 * 60_000;
    private static final long SCAN_TIMEOUT = 15_000;
    // While pairing: how long to look, and how long to wait for others once one was seen.
    private static final long PAIR_SCAN_TIMEOUT = 30_000;
    private static final long PAIR_SETTLE = 3_000;

    // Reply gets the answer to a call, from any thread.
    public interface Reply {
        void send(JSONObject result);
    }

    private final Context context;
    private final SharedPreferences prefs;
    private final ScheduledExecutorService worker = Executors.newSingleThreadScheduledExecutor();
    private ScheduledFuture<?> looking;
    private volatile boolean busy;
    private volatile boolean cancelled;
    private volatile RecorderBle connection;
    // How pairing or copying goes; see RecorderBluetoothState in frontend/src/lib/desktop.ts.
    private final JSONObject state = new JSONObject();

    public static synchronized RecorderController get(Context context) {
        if (instance == null) instance = new RecorderController(context.getApplicationContext());
        return instance;
    }

    private RecorderController(Context context) {
        this.context = context;
        this.prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
        set("phase", "");
    }

    private boolean paired() {
        return !prefs.getString("address", "").isEmpty();
    }

    private boolean enabled() {
        return prefs.getBoolean("enabled", true);
    }

    private static void put(JSONObject o, String key, Object value) {
        try {
            o.put(key, value == null ? JSONObject.NULL : value);
        } catch (JSONException e) {
            throw new IllegalStateException(e);
        }
    }

    private void set(Object... pairs) {
        synchronized (state) {
            for (int i = 0; i < pairs.length; i += 2) put(state, (String) pairs[i], pairs[i + 1]);
        }
    }

    private static JSONObject ok() {
        JSONObject r = new JSONObject();
        put(r, "ok", true);
        return r;
    }

    private static JSONObject failure(String error) {
        JSONObject r = new JSONObject();
        put(r, "ok", false);
        put(r, "error", error);
        return r;
    }

    private JSONObject stateResult() {
        JSONObject r;
        synchronized (state) {
            try {
                r = new JSONObject(state.toString());
            } catch (JSONException e) {
                r = new JSONObject();
            }
        }
        put(r, "ok", true);
        put(r, "paired", paired());
        put(r, "name", prefs.getString("name", ""));
        put(r, "enabled", enabled());
        put(r, "busy", busy);
        put(r, "systemPin", true);
        return r;
    }

    public void handle(MainActivity activity, JSONObject request, Reply reply) {
        String action = request.optString("action");
        switch (action) {
            case "state":
                reply.send(stateResult());
                break;
            case "pair":
                withPermissions(activity, reply, () -> reply.send(startPair(request.optString("name", ""))));
                break;
            case "pin":
                // Android asks for the passkey in its own dialog.
                reply.send(failure("failed"));
                break;
            case "sync":
                if (!paired()) reply.send(failure("not-paired"));
                else if (busy) reply.send(failure("busy"));
                else withPermissions(activity, reply, () -> {
                    worker.execute(() -> look(false));
                    reply.send(ok());
                });
                break;
            case "cancel":
                cancelled = true;
                RecorderBle current = connection;
                if (current != null) current.close();
                reply.send(ok());
                break;
            case "enable":
                if (!paired()) {
                    reply.send(failure("not-paired"));
                    break;
                }
                prefs.edit().putBoolean("enabled", request.optBoolean("enabled")).apply();
                resume();
                reply.send(ok());
                break;
            case "forget":
                prefs.edit().clear().apply();
                set("phase", "", "error", "", "message", "", "devices", new JSONArray());
                reply.send(ok());
                break;
            default:
                reply.send(failure("failed"));
        }
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

    private boolean permitted() {
        for (String p : bluetoothPermissions()) {
            if (ContextCompat.checkSelfPermission(context, p) != PackageManager.PERMISSION_GRANTED) return false;
        }
        return true;
    }

    private void withPermissions(MainActivity activity, Reply reply, Runnable granted) {
        List<String> all = new ArrayList<>(bluetoothPermissions());
        if (Build.VERSION.SDK_INT >= 33) all.add(Manifest.permission.POST_NOTIFICATIONS);
        activity.requestPermissions(all.toArray(new String[0]), result -> {
            for (String p : bluetoothPermissions()) {
                if (!Boolean.TRUE.equals(result.get(p))) {
                    reply.send(failure("permission"));
                    return;
                }
            }
            granted.run();
        });
    }

    // resume starts looking for the recorder (the app came to the front); pause stops it. A copy
    // that runs goes on in the foreground service.
    public synchronized void resume() {
        if (looking != null) looking.cancel(false);
        looking = null;
        if (!paired() || !enabled()) return;
        looking = worker.scheduleWithFixedDelay(() -> look(true), 5_000, LOOK_INTERVAL, TimeUnit.MILLISECONDS);
    }

    public synchronized void pause() {
        if (looking != null) looking.cancel(false);
        looking = null;
    }

    private JSONObject startPair(String wanted) {
        if (busy) return failure("busy");
        busy = true;
        cancelled = false;
        set("phase", "searching", "error", "", "message", "", "devices", new JSONArray(), "copied", 0, "failed", 0);
        worker.execute(() -> {
            try {
                List<RecorderBle.Found> found = RecorderBle.scan(context, null, PAIR_SCAN_TIMEOUT, wanted.isEmpty() ? PAIR_SETTLE : PAIR_SCAN_TIMEOUT);
                RecorderBle.Found chosen = null;
                for (RecorderBle.Found f : found) {
                    if (wanted.isEmpty() ? found.size() == 1 : wanted.equals(f.name)) chosen = f;
                }
                if (chosen == null && found.size() > 1 && wanted.isEmpty()) {
                    JSONArray names = new JSONArray();
                    for (RecorderBle.Found f : found) names.put(f.name.isEmpty() ? f.address : f.name);
                    set("phase", "choose", "devices", names);
                    return;
                }
                if (chosen == null) throw new RecorderBle.RecorderException("not-found", "No recorder in pairing mode was found");
                // Pairing starts fresh: an old bond the recorder no longer has would get in the way.
                RecorderBle.unpair(context, chosen.address);
                set("phase", "pin");
                JSONObject info = new JSONObject();
                put(info, "op", "info");
                RecorderBle ble = null;
                JSONObject r;
                for (int attempt = 1; ; attempt++) {
                    try {
                        ble = RecorderBle.connect(context, chosen.address, attempt == 1);
                        connection = ble;
                        set("phase", "connecting");
                        r = ble.request(info, RecorderRelay.REQUEST_TIMEOUT);
                        break;
                    } catch (RecorderBle.RecorderException e) {
                        if (ble != null) ble.close();
                        ble = null;
                        connection = null;
                        // The pairing went through, but the connection didn't get further (Android
                        // may reset it right after bonding): once more, now as a paired phone.
                        if (attempt >= 2 || cancelled || !RecorderBle.bonded(context, chosen.address)) throw e;
                        Log.w(TAG, "recorder bluetooth: paired, connecting again: " + e.code + ": " + e.getMessage());
                        Thread.sleep(1_000);
                    }
                }
                try {
                    String name = r.optString("name", chosen.name);
                    prefs.edit().putString("address", chosen.address).putString("name", name).putBoolean("enabled", true).apply();
                    Notifications.show(context, Notifications.CHANNEL_RECORDER, context.getString(R.string.recorder_paired_title, name),
                            context.getString(R.string.recorder_paired_body), "/", "recorder-bluetooth");
                    relay(ble, name, false);
                } finally {
                    ble.close();
                    connection = null;
                }
                resume();
            } catch (Exception e) {
                fail(e, false);
            } finally {
                busy = false;
                RecorderSyncService.stop(context);
            }
        });
        return ok();
    }

    // look connects to the paired recorder, if it advertises, and relays its recordings. Quiet
    // (the timer): not finding it is not worth telling.
    private void look(boolean quiet) {
        if (!paired() || !enabled() || busy || !permitted()) return;
        busy = true;
        cancelled = false;
        String address = prefs.getString("address", "");
        String name = prefs.getString("name", "");
        if (!quiet) set("phase", "searching", "error", "", "message", "", "copied", 0, "failed", 0);
        try {
            if (RecorderBle.scan(context, address, SCAN_TIMEOUT, 0).isEmpty()) {
                throw new RecorderBle.RecorderException("not-found", "The recorder wasn't found");
            }
            set("phase", "connecting", "error", "", "message", "");
            RecorderBle ble = RecorderBle.connect(context, address, false);
            connection = ble;
            try {
                relay(ble, name, quiet);
            } finally {
                ble.close();
                connection = null;
            }
        } catch (Exception e) {
            fail(e, quiet);
        } finally {
            busy = false;
            RecorderSyncService.stop(context);
        }
    }

    private void relay(RecorderBle ble, String name, boolean quiet) throws Exception {
        String server = ServerConfig.serverUrl(context);
        if (server == null) throw new RecorderRelay.RelayException("no-server", "No server set up");
        try {
            RecorderSyncService.start(context);
        } catch (RuntimeException e) {
            // Without the service the copy still runs while the app is open.
            Log.w(TAG, "recorder: no foreground service: " + e);
        }
        RecorderRelay.Result result = new RecorderRelay(ble, new DeviceApi(), (phase, current, total, title, bytes, totalBytes) -> {
            set("phase", phase, "current", current, "total", total, "title", title, "bytes", bytes, "totalBytes", totalBytes);
            RecorderSyncService.update(context, phase, current, total, bytes, totalBytes);
        }, () -> cancelled).run(server);
        set("phase", "done", "copied", result.copied, "failed", result.failed, "error", "", "message", String.join("; ", result.errors));
        if (result.copied > 0) {
            Notifications.show(context, Notifications.CHANNEL_RECORDER, context.getString(R.string.recorder_copied_title, name),
                    context.getResources().getQuantityString(R.plurals.recorder_copied_body, result.copied, result.copied), "/", "recorder-bluetooth");
        } else if (result.failed > 0 && !quiet) {
            Notifications.show(context, Notifications.CHANNEL_RECORDER, context.getString(R.string.recorder_failed_title, name),
                    String.join("; ", result.errors), "/", "recorder-bluetooth");
        }
    }

    private void fail(Exception e, boolean quiet) {
        String code = e instanceof RecorderBle.RecorderException ? ((RecorderBle.RecorderException) e).code
                : e instanceof RecorderRelay.RelayException ? ((RecorderRelay.RelayException) e).code
                : "failed";
        if (cancelled) code = "failed";
        Log.w(TAG, "recorder bluetooth: " + code + ": " + e.getMessage());
        set("phase", quiet && "not-found".equals(code) ? "" : "failed", "error", code, "message", String.valueOf(e.getMessage()));
    }
}
