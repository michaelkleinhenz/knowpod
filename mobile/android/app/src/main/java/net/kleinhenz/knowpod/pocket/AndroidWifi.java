package net.kleinhenz.knowpod.pocket;

import android.annotation.SuppressLint;
import android.content.Context;
import android.net.ConnectivityManager;
import android.net.LinkAddress;
import android.net.LinkProperties;
import android.net.MacAddress;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.NetworkRequest;
import android.net.wifi.WifiInfo;
import android.net.wifi.WifiManager;
import android.net.wifi.WifiNetworkSpecifier;
import android.os.Build;
import android.util.Log;
import androidx.annotation.NonNull;
import androidx.annotation.RequiresApi;
import java.io.IOException;
import java.net.Inet4Address;
import java.net.InetSocketAddress;
import java.net.Socket;

// AndroidWifi joins the recorder's network as the Pocket app does: a request for a local-only
// network (WifiNetworkSpecifier, Android 10+). Android asks the user to allow it and joins it
// next to the usual WiFi (or instead of it, on phones with one WiFi radio), without internet
// and without becoming the default network; connections to the recorder are made on it
// explicitly. Releasing the request leaves it.
//
// Once a join worked, the network's BSSID is remembered (if Android tells it, which needs the
// location permission): a request naming SSID and BSSID that the user allowed before is
// allowed again without asking, which matters because the recorder's network is restarted
// after every two recordings.
@RequiresApi(29)
@SuppressLint("MissingPermission") // CHANGE_WIFI_STATE and ACCESS_NETWORK_STATE are granted at install
public final class AndroidWifi implements HostWifi {
    private static final String TAG = "knowpod";

    private final Context context;
    private final PocketSettings settings;
    private final ConnectivityManager connectivity;
    private String ssid;
    private String password;
    private ConnectivityManager.NetworkCallback callback;
    private volatile Network network;
    private volatile boolean addressed;
    private volatile boolean unavailable;

    public AndroidWifi(Context context, PocketSettings settings) {
        this.context = context.getApplicationContext();
        this.settings = settings;
        this.connectivity = (ConnectivityManager) this.context.getSystemService(Context.CONNECTIVITY_SERVICE);
    }

    public static boolean supported() {
        return Build.VERSION.SDK_INT >= 29;
    }

    @Override
    public void setup() throws PocketException {
        WifiManager wifi = (WifiManager) context.getSystemService(Context.WIFI_SERVICE);
        if (wifi == null || connectivity == null) throw new PocketException("wifi-unsupported", "This phone has no WiFi");
        if (!wifi.isWifiEnabled()) throw new PocketException("wifi-off", "WiFi is off");
    }

    @Override
    public void prepare(String ssid, String password) {
        this.ssid = ssid;
        this.password = password;
    }

    private static boolean onRecorderSubnet(LinkProperties properties) {
        if (properties == null) return false;
        for (LinkAddress address : properties.getLinkAddresses()) {
            if (address.getAddress() instanceof Inet4Address
                    && address.getAddress().getHostAddress().startsWith(WifiSession.HOST.substring(0, WifiSession.HOST.lastIndexOf('.') + 1))) {
                return true;
            }
        }
        return false;
    }

    @Override
    public boolean join(String name, long deadline) throws InterruptedException {
        leave();
        WifiNetworkSpecifier.Builder spec = new WifiNetworkSpecifier.Builder()
                .setSsid(name)
                .setWpa2Passphrase(password)
                .setIsHiddenSsid(true);
        String bssid = settings.bssid(name);
        if (bssid != null) {
            try {
                spec.setBssid(MacAddress.fromString(bssid));
            } catch (IllegalArgumentException e) {
                // not an address: ask without it
            }
        }
        NetworkRequest request = new NetworkRequest.Builder()
                .addTransportType(NetworkCapabilities.TRANSPORT_WIFI)
                .removeCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                .setNetworkSpecifier(spec.build())
                .build();
        unavailable = false;
        addressed = false;
        network = null;
        final Object signal = this;
        ConnectivityManager.NetworkCallback cb = newCallback(name, signal);
        callback = cb;
        int timeout = (int) Math.max(1_000, deadline - System.currentTimeMillis());
        try {
            connectivity.requestNetwork(request, cb, timeout);
        } catch (RuntimeException e) {
            Log.w(TAG, "pocket wifi: " + e);
            callback = null;
            return false;
        }
        synchronized (signal) {
            while (!(network != null && addressed) && !unavailable) {
                long left = deadline - System.currentTimeMillis();
                if (left <= 0) break;
                signal.wait(left);
            }
        }
        return network != null && addressed;
    }

    private ConnectivityManager.NetworkCallback newCallback(String name, Object signal) {
        return Build.VERSION.SDK_INT >= 31 ? new Callback(name, signal, ConnectivityManager.NetworkCallback.FLAG_INCLUDE_LOCATION_INFO)
                : new Callback(name, signal);
    }

    // Callback follows the requested network: available, its address, its BSSID, gone.
    private final class Callback extends ConnectivityManager.NetworkCallback {
        private final String name;
        private final Object signal;

        Callback(String name, Object signal) {
            this.name = name;
            this.signal = signal;
        }

        @RequiresApi(31)
        Callback(String name, Object signal, int flags) {
            super(flags);
            this.name = name;
            this.signal = signal;
        }

        @Override
        public void onAvailable(@NonNull Network n) {
            synchronized (signal) {
                network = n;
                addressed = onRecorderSubnet(connectivity.getLinkProperties(n));
                signal.notifyAll();
            }
        }

        @Override
        public void onLinkPropertiesChanged(@NonNull Network n, @NonNull LinkProperties properties) {
            synchronized (signal) {
                if (n.equals(network)) addressed = onRecorderSubnet(properties);
                signal.notifyAll();
            }
        }

        @Override
        public void onCapabilitiesChanged(@NonNull Network n, @NonNull NetworkCapabilities capabilities) {
            if (Build.VERSION.SDK_INT < 31) return;
            if (capabilities.getTransportInfo() instanceof WifiInfo) {
                String bssid = ((WifiInfo) capabilities.getTransportInfo()).getBSSID();
                // Without the location permission Android gives a placeholder.
                if (bssid != null && !bssid.equals("02:00:00:00:00:00")) settings.setBssid(name, bssid);
            }
        }

        @Override
        public void onUnavailable() {
            synchronized (signal) {
                unavailable = true;
                signal.notifyAll();
            }
        }

        @Override
        public void onLost(@NonNull Network n) {
            synchronized (signal) {
                if (n.equals(network)) {
                    network = null;
                    addressed = false;
                }
                signal.notifyAll();
            }
        }
    }

    @Override
    public void leave() {
        ConnectivityManager.NetworkCallback cb = callback;
        callback = null;
        network = null;
        addressed = false;
        if (cb == null) return;
        try {
            connectivity.unregisterNetworkCallback(cb);
        } catch (IllegalArgumentException e) {
            // not registered (any more)
        }
    }

    @Override
    public void restore() {
        // The usual network was never left: releasing the request is all.
        leave();
    }

    @Override
    public Socket connect(String host, int port, int timeout) throws IOException {
        Network n = network;
        if (n == null) throw new IOException("Not on the Pocket's WiFi");
        Socket socket = n.getSocketFactory().createSocket();
        try {
            socket.connect(new InetSocketAddress(host, port), timeout);
        } catch (IOException e) {
            socket.close();
            throw e;
        }
        return socket;
    }
}
