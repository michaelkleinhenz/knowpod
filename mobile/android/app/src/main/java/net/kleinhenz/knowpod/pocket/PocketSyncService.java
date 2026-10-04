package net.kleinhenz.knowpod.pocket;

import android.app.Notification;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;
import android.os.PowerManager;
import androidx.core.app.NotificationCompat;
import androidx.core.app.NotificationManagerCompat;
import androidx.core.app.ServiceCompat;
import androidx.core.content.ContextCompat;
import java.util.Map;
import net.kleinhenz.knowpod.Notifications;
import net.kleinhenz.knowpod.R;

// PocketSyncService keeps the app running while it copies from the Pocket (WifiSync runs in
// PocketController), so the copy goes on with the screen off or the app in the background,
// and shows how it goes in a notification.
public final class PocketSyncService extends Service {
    private static final int ID = 0x504b; // "PK"
    private static long lastUpdate;
    private static String lastPhase = "";

    private PowerManager.WakeLock wakeLock;

    static void start(Context context) {
        ContextCompat.startForegroundService(context, new Intent(context, PocketSyncService.class));
    }

    static void stop(Context context) {
        context.stopService(new Intent(context, PocketSyncService.class));
        // Also when the service couldn't start: its progress notification goes too.
        NotificationManagerCompat.from(context).cancel(ID);
    }

    // update shows the copy's state (WifiSync.state()) in the notification, at most twice a
    // second unless the phase changed.
    static synchronized void update(Context context, Map<String, Object> state) {
        if (!Boolean.TRUE.equals(state.get("running"))) return;
        String phase = String.valueOf(state.get("phase"));
        long now = System.currentTimeMillis();
        if (phase.equals(lastPhase) && now - lastUpdate < 500) return;
        lastPhase = phase;
        lastUpdate = now;
        if (!Notifications.allowed(context)) return;
        try {
            NotificationManagerCompat.from(context).notify(ID, notification(context, state));
        } catch (SecurityException e) {
            // notifications were turned off meanwhile
        }
    }

    private static Notification notification(Context context, Map<String, Object> state) {
        String phase = state == null ? "" : String.valueOf(state.get("phase"));
        int current = state == null ? 0 : (Integer) state.get("current");
        int total = state == null ? 0 : (Integer) state.get("total");
        String text;
        switch (phase) {
            case "downloading":
                text = context.getString("bluetooth".equals(state.get("via")) ? R.string.pocket_progress_downloading_bluetooth
                        : R.string.pocket_progress_downloading, current, total);
                break;
            case "uploading":
                text = context.getString(R.string.pocket_progress_uploading, current, total);
                break;
            case "wifi-starting":
            case "wifi-restarting":
                text = context.getString(R.string.pocket_progress_wifi);
                break;
            default:
                text = context.getString(R.string.pocket_progress_connecting);
        }
        NotificationCompat.Builder builder = new NotificationCompat.Builder(context, Notifications.CHANNEL_POCKET_PROGRESS)
                .setSmallIcon(R.drawable.ic_notification)
                .setContentTitle(context.getString(R.string.pocket_progress_title))
                .setContentText(text)
                .setOngoing(true)
                .setOnlyAlertOnce(true)
                .setContentIntent(Notifications.openIntent(context, null, ID));
        if (state != null && phase.equals("downloading")) {
            long bytes = ((Number) state.get("bytes")).longValue();
            long totalBytes = ((Number) state.get("totalBytes")).longValue();
            if (totalBytes > 0) builder.setProgress(1000, (int) Math.min(1000, bytes * 1000 / totalBytes), false);
        } else {
            builder.setProgress(0, 0, true);
        }
        return builder.build();
    }

    @Override
    public void onCreate() {
        super.onCreate();
        Notifications.createChannels(this);
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        int type = Build.VERSION.SDK_INT >= 29 ? ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE : 0;
        ServiceCompat.startForeground(this, ID, notification(this, null), type);
        if (wakeLock == null) {
            PowerManager power = (PowerManager) getSystemService(POWER_SERVICE);
            wakeLock = power.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "knowpod:pocket-sync");
            // A copy of many hours of recordings takes minutes; never hold it longer than this.
            wakeLock.acquire(60 * 60 * 1000L);
        }
        return START_NOT_STICKY;
    }

    @Override
    public void onDestroy() {
        if (wakeLock != null && wakeLock.isHeld()) wakeLock.release();
        wakeLock = null;
        ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE);
        super.onDestroy();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }
}
