package net.kleinhenz.knowpod.pocket;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URI;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Function;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

// ServerApi calls the knowpod server as the signed-in user, with the web app's session cookie
// (the WebView's): which recordings aren't notes yet, and uploading one. The same endpoints the
// desktop app's copy uses (desktop/src/pocket-wifi-sync.js).
public class ServerApi {
    // HttpException is an answer of the server other than 2xx.
    public static final class HttpException extends IOException {
        public final int status;

        HttpException(int status, String body) {
            super(messageOf(status, body));
            this.status = status;
        }

        private static String messageOf(int status, String body) {
            try {
                String message = new JSONObject(body).optString("message", "");
                if (!message.isEmpty()) return message;
            } catch (JSONException ignored) {
                // not JSON
            }
            return "HTTP " + status;
        }
    }

    private final Function<String, String> cookies;

    // cookies gives the Cookie header for a URL (null for none).
    public ServerApi(Function<String, String> cookies) {
        this.cookies = cookies;
    }

    // newFiles returns which of names (recording file names) aren't notes yet.
    public List<String> newFiles(String server, List<String> names) throws IOException {
        JSONObject body = new JSONObject();
        try {
            body.put("files", new JSONArray(names));
        } catch (JSONException e) {
            throw new IOException(e);
        }
        HttpURLConnection c = open(server + "/api/v1/me/pocket/device/check");
        c.setRequestProperty("Content-Type", "application/json");
        byte[] bytes = body.toString().getBytes(StandardCharsets.UTF_8);
        c.setFixedLengthStreamingMode(bytes.length);
        try (OutputStream out = c.getOutputStream()) {
            out.write(bytes);
        }
        JSONObject answer = read(c);
        List<String> fresh = new ArrayList<>();
        JSONArray files = answer.optJSONArray("files");
        if (files != null) for (int i = 0; i < files.length(); i++) fresh.add(files.optString(i));
        return fresh;
    }

    // upload sends a recording, streamed (recordings can be hours long), and returns the id of
    // its note. 409: it already is one.
    public String upload(String server, File file, String name) throws IOException {
        HttpURLConnection c = open(server + "/api/v1/me/pocket/device/files");
        c.setRequestProperty("Content-Type", "audio/mpeg");
        c.setRequestProperty("X-Filename", URLEncoder.encode(name, "UTF-8").replace("+", "%20"));
        c.setFixedLengthStreamingMode(file.length());
        try (OutputStream out = c.getOutputStream(); InputStream in = new FileInputStream(file)) {
            byte[] buffer = new byte[64 * 1024];
            int n;
            while ((n = in.read(buffer)) >= 0) out.write(buffer, 0, n);
        }
        return read(c).optString("id", "");
    }

    private HttpURLConnection open(String url) throws IOException {
        HttpURLConnection c = (HttpURLConnection) URI.create(url).toURL().openConnection();
        c.setRequestMethod("POST");
        c.setDoOutput(true);
        c.setConnectTimeout(15_000);
        c.setReadTimeout(120_000);
        c.setInstanceFollowRedirects(false);
        String cookie = cookies.apply(url);
        if (cookie != null && !cookie.isEmpty()) c.setRequestProperty("Cookie", cookie);
        return c;
    }

    private static JSONObject read(HttpURLConnection c) throws IOException {
        try {
            int status = c.getResponseCode();
            InputStream in = status >= 400 ? c.getErrorStream() : c.getInputStream();
            String text = "";
            if (in != null) {
                try (InputStream body = in) {
                    ByteArrayOutputStream out = new ByteArrayOutputStream();
                    byte[] buffer = new byte[8192];
                    int n;
                    while ((n = body.read(buffer)) >= 0) out.write(buffer, 0, n);
                    text = out.toString("UTF-8");
                }
            }
            if (status < 200 || status >= 300) throw new HttpException(status, text);
            if (text.trim().isEmpty()) return new JSONObject();
            try {
                return new JSONObject(text);
            } catch (JSONException e) {
                return new JSONObject();
            }
        } finally {
            c.disconnect();
        }
    }
}
