package net.kleinhenz.knowpod;

import android.Manifest;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.os.Build;
import androidx.core.app.NotificationCompat;
import androidx.core.app.NotificationManagerCompat;
import androidx.core.content.ContextCompat;
import org.json.JSONObject;

// Notifications shows the server's notifications ({title, body, url, tag}, see
// backend/internal/service/notifications.go), which the web app hands over while it runs
// (frontend/src/lib/desktop.ts: Web Push doesn't reach a WebView), and those of the copies from
// the Pocket and the knowpod recorder.
// Clicking one opens its page in the app.
public final class Notifications {
    public static final String CHANNEL_REMINDERS = "reminders";
    public static final String CHANNEL_POCKET = "pocket";
    public static final String CHANNEL_POCKET_PROGRESS = "pocket-progress";
    public static final String CHANNEL_RECORDER = "recorder";
    public static final String CHANNEL_RECORDER_PROGRESS = "recorder-progress";
    public static final String EXTRA_URL = "net.kleinhenz.knowpod.URL";

    private Notifications() {}

    // createChannels sets up the channels (Android 8+); safe to call again.
    public static void createChannels(Context context) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return;
        NotificationManager manager = context.getSystemService(NotificationManager.class);
        manager.createNotificationChannel(new NotificationChannel(CHANNEL_REMINDERS,
                context.getString(R.string.channel_reminders), NotificationManager.IMPORTANCE_DEFAULT));
        manager.createNotificationChannel(new NotificationChannel(CHANNEL_POCKET,
                context.getString(R.string.channel_pocket), NotificationManager.IMPORTANCE_DEFAULT));
        manager.createNotificationChannel(new NotificationChannel(CHANNEL_POCKET_PROGRESS,
                context.getString(R.string.channel_pocket_progress), NotificationManager.IMPORTANCE_LOW));
        manager.createNotificationChannel(new NotificationChannel(CHANNEL_RECORDER,
                context.getString(R.string.channel_recorder), NotificationManager.IMPORTANCE_DEFAULT));
        manager.createNotificationChannel(new NotificationChannel(CHANNEL_RECORDER_PROGRESS,
                context.getString(R.string.channel_recorder_progress), NotificationManager.IMPORTANCE_LOW));
    }

    public static boolean allowed(Context context) {
        return Build.VERSION.SDK_INT < 33
                || ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED;
    }

    // openIntent opens the app at path (a page of the server, "/…").
    public static PendingIntent openIntent(Context context, String path, int requestCode) {
        Intent intent = new Intent(context, MainActivity.class);
        intent.setFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_SINGLE_TOP);
        if (path != null && path.startsWith("/") && !path.startsWith("//")) intent.putExtra(EXTRA_URL, path);
        return PendingIntent.getActivity(context, requestCode, intent, PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    }

    // show shows a notification from the server; a newer one with the same tag replaces it.
    public static void show(Context context, JSONObject message) {
        show(context, CHANNEL_REMINDERS, clip(message.optString("title", "knowpod"), 200), clip(message.optString("body", ""), 500),
                message.optString("url", ""), message.optString("tag", ""));
    }

    public static void show(Context context, String channel, String title, String body, String url, String tag) {
        if (!allowed(context)) return;
        String key = tag == null || tag.isEmpty() ? "untagged-" + System.nanoTime() : tag;
        NotificationCompat.Builder builder = new NotificationCompat.Builder(context, channel)
                .setSmallIcon(R.drawable.ic_notification)
                .setContentTitle(title.isEmpty() ? "knowpod" : title)
                .setContentText(body)
                .setStyle(new NotificationCompat.BigTextStyle().bigText(body))
                .setAutoCancel(true)
                .setContentIntent(openIntent(context, url, key.hashCode()));
        try {
            NotificationManagerCompat.from(context).notify(key, 1, builder.build());
        } catch (SecurityException e) {
            // notifications were turned off meanwhile
        }
    }

    private static String clip(String text, int max) {
        return text.length() > max ? text.substring(0, max) : text;
    }
}
