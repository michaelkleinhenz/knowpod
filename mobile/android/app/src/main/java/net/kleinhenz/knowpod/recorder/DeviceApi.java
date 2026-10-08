package net.kleinhenz.knowpod.recorder;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URI;
import java.util.Map;

// DeviceApi calls the backend's device API for RecorderRelay, with the headers it gives (the
// recorder's device token). No cookies: the relay acts as the recorder, not as the user.
// Android's HttpURLConnection also does PATCH.
final class DeviceApi implements RecorderRelay.Http {
    @Override
    public RecorderRelay.Response call(String method, String url, Map<String, String> headers, byte[] body) throws IOException {
        HttpURLConnection c = (HttpURLConnection) URI.create(url).toURL().openConnection();
        try {
            c.setRequestMethod(method);
            c.setConnectTimeout(15_000);
            c.setReadTimeout(180_000);
            c.setInstanceFollowRedirects(false);
            c.setUseCaches(false);
            for (Map.Entry<String, String> h : headers.entrySet()) c.setRequestProperty(h.getKey(), h.getValue());
            if (body != null) {
                c.setDoOutput(true);
                c.setFixedLengthStreamingMode(body.length);
                try (OutputStream out = c.getOutputStream()) {
                    out.write(body);
                }
            }
            int status = c.getResponseCode();
            String text = "";
            InputStream in = status >= 400 ? c.getErrorStream() : c.getInputStream();
            if (in != null) {
                try (InputStream stream = in) {
                    ByteArrayOutputStream out = new ByteArrayOutputStream();
                    byte[] buffer = new byte[8192];
                    int n;
                    while ((n = stream.read(buffer)) >= 0) out.write(buffer, 0, n);
                    text = out.toString("UTF-8");
                }
            }
            long offset = -1;
            String header = c.getHeaderField("Upload-Offset");
            if (header != null) {
                try {
                    offset = Long.parseLong(header.trim());
                } catch (NumberFormatException e) {
                    // not a number
                }
            }
            return new RecorderRelay.Response(status, text, offset);
        } finally {
            c.disconnect();
        }
    }
}
