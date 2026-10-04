package net.kleinhenz.knowpod;

import android.content.Context;
import android.content.SharedPreferences;
import android.net.Uri;

// ServerConfig keeps the knowpod server the app loads: the one entered on the setup page, else
// the one baked in at build time (-PdefaultServerUrl=…, see app/build.gradle). Like the
// desktop app's (desktop/src/main.js).
public final class ServerConfig {
    private static final String PREFS = "knowpod";
    private static final String KEY = "serverUrl";

    private ServerConfig() {}

    private static SharedPreferences prefs(Context context) {
        return context.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    // normalize makes an origin of what the user typed ("knowpod.example.com" →
    // "https://knowpod.example.com"), or returns null if it isn't an http(s) address.
    public static String normalize(String input) {
        String text = input == null ? "" : input.trim();
        if (text.isEmpty()) return null;
        if (!text.matches("(?i)^[a-z][a-z0-9+.-]*://.*")) text = "https://" + text;
        Uri uri = Uri.parse(text);
        String scheme = uri.getScheme() == null ? "" : uri.getScheme().toLowerCase();
        String host = uri.getHost();
        if (!(scheme.equals("https") || scheme.equals("http")) || host == null || host.isEmpty()) return null;
        if (!host.matches("[A-Za-z0-9.\\-\\[\\]:]+")) return null;
        int port = uri.getPort();
        boolean defaultPort = port == -1 || (scheme.equals("https") && port == 443) || (scheme.equals("http") && port == 80);
        return scheme + "://" + host.toLowerCase() + (defaultPort ? "" : ":" + port);
    }

    // serverUrl is the server to load, or null.
    public static String serverUrl(Context context) {
        String saved = normalize(prefs(context).getString(KEY, null));
        if (saved != null) return saved;
        return normalize(context.getString(R.string.default_server_url));
    }

    public static void setServerUrl(Context context, String url) {
        prefs(context).edit().putString(KEY, url).apply();
    }

    // originOf is the origin of url ("https://host[:port]"), or null.
    public static String originOf(String url) {
        if (url == null || !url.matches("(?i)^https?://.*")) return null;
        return normalize(url);
    }

    // isServerUrl says whether url is a page of the server itself.
    public static boolean isServerUrl(Context context, String url) {
        String server = serverUrl(context);
        String origin = originOf(url);
        return server != null && server.equals(origin);
    }
}
