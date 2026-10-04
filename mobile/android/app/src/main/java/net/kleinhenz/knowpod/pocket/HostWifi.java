package net.kleinhenz.knowpod.pocket;

import java.io.IOException;
import java.net.Socket;

// HostWifi moves this device onto the recorder's network and back (desktop: NetworkManager or
// netsh in desktop/src/pocket-wifi.js; the phone: AndroidWifi).
public interface HostWifi {
    // setup checks that this device can join the recorder's network at all ('wifi-setup',
    // 'wifi-unsupported').
    void setup() throws PocketException;

    // prepare takes the network's name and password (from APP&WIFI).
    void prepare(String ssid, String password) throws PocketException;

    // join joins the network, giving up at deadline (System.currentTimeMillis()); returns
    // whether this device has an address on it. It must never ask again while a join is
    // under way: the recorder drops its transfer socket when its client leaves.
    boolean join(String ssid, long deadline) throws InterruptedException;

    // leave leaves the network (the recorder's access point is being restarted).
    void leave();

    // restore puts this device back on its usual network. Never throws.
    void restore();

    // connect opens a TCP connection to host:port over the recorder's network.
    Socket connect(String host, int port, int timeout) throws IOException;
}
