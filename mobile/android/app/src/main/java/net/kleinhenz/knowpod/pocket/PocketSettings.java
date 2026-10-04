package net.kleinhenz.knowpod.pocket;

import android.content.Context;
import android.content.SharedPreferences;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.util.Base64;
import java.nio.charset.StandardCharsets;
import java.security.KeyStore;
import java.util.LinkedHashSet;
import java.util.Set;
import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;
import org.json.JSONArray;
import org.json.JSONException;

// PocketSettings keeps the recorder's Bluetooth address and its session key, and the files
// already copied, like the desktop app (desktop/src/pocket-bluetooth.js, pocket.js). The key
// is a secret (its first 8 characters are also the recorder's WiFi password), so it's
// encrypted with a key of the Android Keystore, and never handed to a page.
public final class PocketSettings {
    private static final String PREFS = "pocket";
    private static final String KEYSTORE = "AndroidKeyStore";
    private static final String KEY_ALIAS = "knowpod-pocket";
    // How many copied files are remembered per server.
    private static final int MAX_REMEMBERED = 20_000;
    // The BSSIDs of recorders' networks, by SSID (see AndroidWifi).
    private static final String BSSID_PREFIX = "bssid:";

    private final SharedPreferences prefs;

    public PocketSettings(Context context) {
        prefs = context.getApplicationContext().getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    private static final String HEX = "[0-9a-fA-F]";

    // normalizeAddress makes "aa-bb-cc-dd-ee-ff" or "aabbccddeeff" into "AA:BB:CC:DD:EE:FF", or
    // returns null if it isn't a Bluetooth address.
    public static String normalizeAddress(String input) {
        String text = input == null ? "" : input.trim();
        if (!text.matches(HEX + "{2}([:-]?" + HEX + "{2}){5}")) return null;
        String hex = text.replaceAll("[^0-9a-fA-F]", "").toUpperCase();
        StringBuilder out = new StringBuilder();
        for (int i = 0; i < 12; i += 2) {
            if (i > 0) out.append(':');
            out.append(hex, i, i + 2);
        }
        return out.toString();
    }

    // A session key is 16 characters; "&" would end the command it's sent in.
    public static boolean validSessionKey(String key) {
        return key.matches("[\\x21-\\x25\\x27-\\x7e]{8,64}");
    }

    public String address() {
        return prefs.getString("address", "");
    }

    public String sessionKey() {
        String stored = prefs.getString("sessionKey", null);
        if (stored == null) return "";
        try {
            byte[] data = Base64.decode(stored, Base64.NO_WRAP);
            Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
            cipher.init(Cipher.DECRYPT_MODE, secretKey(), new GCMParameterSpec(128, data, 0, 12));
            return new String(cipher.doFinal(data, 12, data.length - 12), StandardCharsets.UTF_8);
        } catch (Exception e) {
            return ""; // e.g. the keystore was reset
        }
    }

    public boolean configured() {
        return !address().isEmpty() && !sessionKey().isEmpty();
    }

    // save changes the settings: address "" and sessionKey "" remove them, null keeps them.
    // Returns an error code (invalid-address, invalid-key, failed) or null.
    public String save(String address, String sessionKey) {
        SharedPreferences.Editor edit = prefs.edit();
        if (address != null) {
            String normalized = address.isEmpty() ? "" : normalizeAddress(address);
            if (normalized == null) return "invalid-address";
            edit.putString("address", normalized);
        }
        if (sessionKey != null) {
            String text = sessionKey.trim();
            if (text.isEmpty()) {
                edit.remove("sessionKey");
            } else {
                if (!validSessionKey(text)) return "invalid-key";
                try {
                    Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
                    cipher.init(Cipher.ENCRYPT_MODE, secretKey());
                    byte[] iv = cipher.getIV();
                    byte[] sealed = cipher.doFinal(text.getBytes(StandardCharsets.UTF_8));
                    byte[] data = new byte[iv.length + sealed.length];
                    System.arraycopy(iv, 0, data, 0, iv.length);
                    System.arraycopy(sealed, 0, data, iv.length, sealed.length);
                    edit.putString("sessionKey", Base64.encodeToString(data, Base64.NO_WRAP));
                } catch (Exception e) {
                    return "failed";
                }
            }
        }
        edit.apply();
        return null;
    }

    private static SecretKey secretKey() throws Exception {
        KeyStore store = KeyStore.getInstance(KEYSTORE);
        store.load(null);
        KeyStore.Entry entry = store.getEntry(KEY_ALIAS, null);
        if (entry instanceof KeyStore.SecretKeyEntry) return ((KeyStore.SecretKeyEntry) entry).getSecretKey();
        KeyGenerator generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE);
        generator.init(new KeyGenParameterSpec.Builder(KEY_ALIAS, KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build());
        return generator.generateKey();
    }

    // The files already copied to a server are remembered, so they aren't asked about again
    // and a note deleted for good isn't copied again either.
    private static String rememberedKey(String server) {
        return "copied:" + server;
    }

    public synchronized Set<String> remembered(String server) {
        Set<String> names = new LinkedHashSet<>();
        try {
            JSONArray list = new JSONArray(prefs.getString(rememberedKey(server), "[]"));
            for (int i = 0; i < list.length(); i++) names.add(list.getString(i));
        } catch (JSONException e) {
            // start over
        }
        return names;
    }

    public synchronized void remember(String server, String name) {
        Set<String> names = remembered(server);
        names.remove(name);
        names.add(name);
        JSONArray list = new JSONArray();
        int skip = Math.max(0, names.size() - MAX_REMEMBERED);
        for (String n : names) {
            if (skip-- > 0) continue;
            list.put(n);
        }
        prefs.edit().putString(rememberedKey(server), list.toString()).apply();
    }

    public String bssid(String ssid) {
        return prefs.getString(BSSID_PREFIX + ssid, null);
    }

    public void setBssid(String ssid, String bssid) {
        prefs.edit().putString(BSSID_PREFIX + ssid, bssid).apply();
    }
}
