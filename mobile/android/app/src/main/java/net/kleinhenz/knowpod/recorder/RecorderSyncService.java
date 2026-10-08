package net.kleinhenz.knowpod.recorder;

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
import net.kleinhenz.knowpod.Notifications;
import net.kleinhenz.knowpod.R;

// RecorderSyncService keeps the app running while it copies from the knowpod recorder over
// Bluetooth (RecorderController), so the copy goes on with the screen off or the app in the
// background, and shows how it goes in a notification. Like pocket/PocketSyncService.
public final class RecorderSyncService extends Service {
    private static final int ID = 0x4b52; // "KR"
    private static volatile boolean running;
    private static long lastUpdate;

    private PowerManager.WakeLock wakeLock;

    static void start(Context context) {
        running = true;
        ContextCompat.startForegroundService(context, new Intent(context, RecorderSyncService.class));
    }

    static void stop(Context context) {
        if (!running) return;
        running = false;
        context.stopService(new Intent(context, RecorderSyncService.class));
        NotificationManagerCompat.from(context).cancel(ID);
    }

    // update shows the copy's progress, at most twice a second.
    static synchronized void update(Context context, String phase, int current, int total, long bytes, long totalBytes) {
        if (!running) return;
        long now = System.currentTimeMillis();
        if (now - lastUpdate < 500) return;
        lastUpdate = now;
        if (!Notifications.allowed(context)) return;
        try {
            NotificationManagerCompat.from(context).notify(ID, notification(context, phase, current, total, bytes, totalBytes));
        } catch (SecurityException e) {
            // notifications were turned off meanwhile
        }
    }

    private static Notification notification(Context context, String phase, int current, int total, long bytes, long totalBytes) {
        String text = "uploading".equals(phase) || "preparing".equals(phase)
                ? context.getString(R.string.recorder_progress_uploading, current, total)
                : context.getString(R.string.recorder_progress_connecting);
        NotificationCompat.Builder builder = new NotificationCompat.Builder(context, Notifications.CHANNEL_RECORDER_PROGRESS)
                .setSmallIcon(R.drawable.ic_notification)
                .setContentTitle(context.getString(R.string.recorder_progress_title))
                .setContentText(text)
                .setOngoing(true)
                .setOnlyAlertOnce(true)
                .setContentIntent(Notifications.openIntent(context, null, ID));
        if ("uploading".equals(phase) && totalBytes > 0) builder.setProgress(1000, (int) Math.min(1000, bytes * 1000 / totalBytes), false);
        else builder.setProgress(0, 0, true);
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
        ServiceCompat.startForeground(this, ID, notification(this, "", 0, 0, 0, 0), type);
        if (wakeLock == null) {
            PowerManager power = (PowerManager) getSystemService(POWER_SERVICE);
            wakeLock = power.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "knowpod:recorder-sync");
            // Hours of recordings take a while over Bluetooth; never hold it longer than this.
            wakeLock.acquire(2 * 60 * 60 * 1000L);
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
