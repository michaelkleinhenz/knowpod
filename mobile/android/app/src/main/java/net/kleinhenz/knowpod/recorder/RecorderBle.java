package net.kleinhenz.knowpod.recorder;

import android.annotation.SuppressLint;
import android.bluetooth.BluetoothAdapter;
import android.bluetooth.BluetoothDevice;
import android.bluetooth.BluetoothGatt;
import android.bluetooth.BluetoothGattCallback;
import android.bluetooth.BluetoothGattCharacteristic;
import android.bluetooth.BluetoothGattDescriptor;
import android.bluetooth.BluetoothGattService;
import android.bluetooth.BluetoothManager;
import android.bluetooth.BluetoothProfile;
import android.bluetooth.BluetoothStatusCodes;
import android.bluetooth.le.BluetoothLeScanner;
import android.bluetooth.le.ScanCallback;
import android.bluetooth.le.ScanFilter;
import android.bluetooth.le.ScanResult;
import android.bluetooth.le.ScanSettings;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.os.Build;
import android.os.ParcelUuid;
import android.util.Log;
import androidx.annotation.NonNull;
import androidx.core.content.ContextCompat;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import org.json.JSONException;
import org.json.JSONObject;

// RecorderBle is the Bluetooth LE connection to a knowpod recorder (the ESP32 gadget, esp32/) on
// the phone, the counterpart of desktop/src/recorder-ble.js: it finds the recorder by its
// transfer service, connects, pairs if needed (Android shows the passkey dialog), and carries the
// requests of docs/ble-transfer.md. Android runs one GATT operation at a time, so each waits for
// the one before.
@SuppressLint("MissingPermission") // RecorderController asks for the permissions first
public final class RecorderBle implements RecorderRelay.Link {
    static final UUID SERVICE = UUID.fromString("6b6e7000-0b1e-4d0a-9c3e-6b6e6f77706f");
    static final UUID CONTROL = UUID.fromString("6b6e7001-0b1e-4d0a-9c3e-6b6e6f77706f");
    static final UUID DATA = UUID.fromString("6b6e7002-0b1e-4d0a-9c3e-6b6e6f77706f");
    private static final String TAG = "knowpod";
    private static final UUID CCCD = UUID.fromString("00002902-0000-1000-8000-00805f9b34fb");

    private static final long CONNECT_TIMEOUT = 20_000;
    private static final long OP_TIMEOUT = 10_000;
    // How long pairing may take (the user types the passkey).
    private static final long BOND_TIMEOUT = 120_000;
    private static final long READ_TIMEOUT = 60_000;

    // RecorderException carries a code for the app to explain (see RecorderController).
    public static final class RecorderException extends IOException {
        public final String code;

        RecorderException(String code, String message) {
            super(message);
            this.code = code;
        }
    }

    // Found is a recorder seen while scanning.
    public static final class Found {
        public final String address;
        public final String name;

        Found(String address, String name) {
            this.address = address;
            this.name = name;
        }
    }

    private final Context context;
    private final Object lock = new Object();
    private BluetoothGatt gatt;
    private BluetoothGattCharacteristic control;
    private volatile boolean connected;
    // The GATT operation waited for and its outcome (a GATT status), or null while it runs.
    private String waiting;
    private Integer outcome;
    // The response being received (guarded by lock)
    private final ByteArrayOutputStream fragments = new ByteArrayOutputStream();
    private JSONObject response;
    // The read collecting file bytes (guarded by lock)
    private ByteArrayOutputStream collected;
    private long next;
    private boolean gap;
    private int packets;       // data notifications of the current read
    private long firstAt = -1; // offset of its first one
    private int late;          // data notifications that came when no read was waiting

    private RecorderBle(Context context) {
        this.context = context.getApplicationContext();
    }

    private static BluetoothAdapter adapter(Context context) throws RecorderException {
        BluetoothManager manager = (BluetoothManager) context.getSystemService(Context.BLUETOOTH_SERVICE);
        BluetoothAdapter adapter = manager == null ? null : manager.getAdapter();
        if (adapter == null) throw new RecorderException("unsupported", "This phone has no Bluetooth");
        if (!adapter.isEnabled()) throw new RecorderException("unsupported", "Bluetooth is off");
        return adapter;
    }

    // scan looks for recorders advertising the transfer service for up to timeout ms: the one at
    // address (if given), else all found, waiting settle ms after the first for others.
    public static List<Found> scan(Context context, String address, long timeout, long settle) throws RecorderException, InterruptedException {
        BluetoothLeScanner scanner = adapter(context).getBluetoothLeScanner();
        if (scanner == null) throw new RecorderException("unsupported", "Bluetooth is off");
        Map<String, Found> found = new LinkedHashMap<>();
        int[] failed = {0};
        ScanCallback callback = new ScanCallback() {
            @Override
            public void onScanResult(int callbackType, ScanResult result) {
                String name = result.getScanRecord() != null ? result.getScanRecord().getDeviceName() : null;
                synchronized (found) {
                    found.put(result.getDevice().getAddress(), new Found(result.getDevice().getAddress(), name == null ? "" : name));
                    found.notifyAll();
                }
            }

            @Override
            public void onScanFailed(int errorCode) {
                synchronized (found) {
                    failed[0] = errorCode;
                    found.notifyAll();
                }
            }
        };
        ScanFilter.Builder filter = new ScanFilter.Builder().setServiceUuid(new ParcelUuid(SERVICE));
        if (address != null && !address.isEmpty()) filter.setDeviceAddress(address);
        ScanSettings settings = new ScanSettings.Builder().setScanMode(ScanSettings.SCAN_MODE_LOW_LATENCY).build();
        scanner.startScan(Collections.singletonList(filter.build()), settings, callback);
        try {
            long end = System.currentTimeMillis() + timeout;
            long firstAt = 0;
            synchronized (found) {
                while (failed[0] == 0) {
                    long now = System.currentTimeMillis();
                    if (!found.isEmpty() && firstAt == 0) firstAt = now;
                    if (firstAt != 0 && now - firstAt >= settle) break;
                    long left = Math.min(end - now, firstAt != 0 ? firstAt + settle - now : end - now);
                    if (left <= 0) break;
                    found.wait(left);
                }
            }
        } finally {
            try {
                scanner.stopScan(callback);
            } catch (IllegalStateException e) {
                // Bluetooth went off meanwhile
            }
        }
        if (failed[0] != 0) throw new RecorderException("failed", "Scanning failed (" + failed[0] + ")");
        synchronized (found) {
            return new ArrayList<>(found.values());
        }
    }

    // connect connects to the recorder at address and subscribes to its characteristics. With
    // pair, a recorder the phone has no bond with is paired (Android asks for the passkey);
    // without, such a recorder is refused ('auth'), so a background look never pops up a dialog.
    public static RecorderBle connect(Context context, String address, boolean pair) throws IOException, InterruptedException {
        RecorderBle ble = new RecorderBle(context);
        try {
            ble.open(adapter(context).getRemoteDevice(address), pair);
        } catch (SecurityException e) {
            ble.close();
            throw new RecorderException("permission", e.getMessage());
        } catch (IOException | InterruptedException | RuntimeException e) {
            ble.close();
            throw e;
        }
        return ble;
    }

    private void open(BluetoothDevice device, boolean pair) throws IOException, InterruptedException {
        if (!pair && device.getBondState() != BluetoothDevice.BOND_BONDED) {
            throw new RecorderException("auth", "The recorder isn't paired with this phone");
        }
        // A first connection sometimes fails at once (status 133); a second try usually works.
        for (int attempt = 1; ; attempt++) {
            begin("connect");
            gatt = device.connectGatt(context, false, callback, BluetoothDevice.TRANSPORT_LE);
            if (gatt == null) throw new RecorderException("failed", "Couldn't connect");
            Integer status = await("connect", CONNECT_TIMEOUT);
            if (status != null && status == BluetoothGatt.GATT_SUCCESS && connected) break;
            gatt.close();
            gatt = null;
            if (attempt >= 2) {
                throw status == null ? new RecorderException("timeout", "Connecting took too long")
                        : new RecorderException("failed", "Connecting failed (status " + status + ")");
            }
            Thread.sleep(500);
        }
        boolean bondedNow = device.getBondState() != BluetoothDevice.BOND_BONDED;
        if (bondedNow) bond(device);
        begin("mtu");
        if (gatt.requestMtu(517)) await("mtu", OP_TIMEOUT);
        // Right after bonding Android reads the services itself; asking at the same time can
        // come back without them. Give it that time first.
        if (bondedNow) Thread.sleep(1_600);
        BluetoothGattService service = discover();
        if (service == null) {
            // A stale cache (from before the recorder's firmware or bond changed): drop it, ask again.
            refreshCache();
            Thread.sleep(500);
            service = discover();
        }
        control = service == null ? null : service.getCharacteristic(CONTROL);
        BluetoothGattCharacteristic data = service == null ? null : service.getCharacteristic(DATA);
        if (control == null || data == null) throw new RecorderException("failed", "This device isn't a knowpod recorder");
        subscribe(control);
        subscribe(data);
        // A short connection interval: recordings come several times faster.
        gatt.requestConnectionPriority(BluetoothGatt.CONNECTION_PRIORITY_HIGH);
    }

    // discover reads the recorder's services and returns the transfer service, null if missing.
    private BluetoothGattService discover() throws IOException, InterruptedException {
        if (!connected || gatt == null) throw new RecorderException("disconnected", "The recorder disconnected");
        begin("services");
        if (!gatt.discoverServices()) throw new RecorderException("failed", "Couldn't read the services");
        Integer discovered = await("services", 15_000);
        if (discovered == null || discovered != BluetoothGatt.GATT_SUCCESS) throw new RecorderException("failed", "Couldn't read the services");
        return gatt.getService(SERVICE);
    }

    // refreshCache drops Android's cached services of the recorder (a hidden call; best effort).
    private void refreshCache() {
        try {
            gatt.getClass().getMethod("refresh").invoke(gatt);
        } catch (ReflectiveOperationException | RuntimeException e) {
            // not there on this phone: the next discovery may still read them fresh
        }
    }

    // bonded tells whether the phone has a bond with the recorder at address.
    public static boolean bonded(Context context, String address) {
        try {
            return adapter(context).getRemoteDevice(address).getBondState() == BluetoothDevice.BOND_BONDED;
        } catch (RecorderException | RuntimeException e) {
            return false;
        }
    }

    // unpair drops the phone's bond with the recorder at address, so pairing starts fresh: a bond
    // the recorder no longer has (it forgot its apps, or was erased) makes Android connect as
    // paired, and the recorder's passkey then interrupts the first request. A hidden call; best
    // effort, waiting briefly until Android has dropped it.
    public static void unpair(Context context, String address) throws InterruptedException {
        BluetoothDevice device;
        try {
            device = adapter(context).getRemoteDevice(address);
            if (device.getBondState() == BluetoothDevice.BOND_NONE) return;
            device.getClass().getMethod("removeBond").invoke(device);
        } catch (RecorderException | ReflectiveOperationException | RuntimeException e) {
            return;
        }
        long end = System.currentTimeMillis() + 3_000;
        while (device.getBondState() != BluetoothDevice.BOND_NONE && System.currentTimeMillis() < end) Thread.sleep(100);
    }

    // bond pairs with the recorder: the recorder asks for it right after connecting, so Android
    // may already be at it; else it is started here. Android shows the passkey dialog.
    private void bond(BluetoothDevice device) throws IOException, InterruptedException {
        Object bonded = new Object();
        int[] state = {device.getBondState()};
        BroadcastReceiver receiver = new BroadcastReceiver() {
            @Override
            @SuppressWarnings("deprecation") // the call before Android 13
            public void onReceive(Context c, Intent intent) {
                BluetoothDevice d = intent.getParcelableExtra(BluetoothDevice.EXTRA_DEVICE);
                if (d == null || !d.getAddress().equals(device.getAddress())) return;
                synchronized (bonded) {
                    state[0] = intent.getIntExtra(BluetoothDevice.EXTRA_BOND_STATE, BluetoothDevice.BOND_NONE);
                    bonded.notifyAll();
                }
            }
        };
        ContextCompat.registerReceiver(context, receiver, new IntentFilter(BluetoothDevice.ACTION_BOND_STATE_CHANGED),
                ContextCompat.RECEIVER_EXPORTED);
        try {
            synchronized (bonded) {
                state[0] = device.getBondState();
                if (state[0] == BluetoothDevice.BOND_NONE && !device.createBond()
                        && device.getBondState() == BluetoothDevice.BOND_NONE) {
                    throw new RecorderException("auth", "Couldn't start pairing");
                }
                long end = System.currentTimeMillis() + BOND_TIMEOUT;
                boolean started = state[0] == BluetoothDevice.BOND_BONDING;
                while (state[0] != BluetoothDevice.BOND_BONDED && connected) {
                    if (state[0] == BluetoothDevice.BOND_BONDING) started = true;
                    else if (started) break; // back to none: refused or a wrong code
                    long left = end - System.currentTimeMillis();
                    if (left <= 0) break;
                    bonded.wait(Math.min(left, 500));
                    if (state[0] == BluetoothDevice.BOND_NONE && !started) state[0] = device.getBondState();
                }
                if (state[0] != BluetoothDevice.BOND_BONDED) throw new RecorderException("auth", "Pairing failed");
            }
        } finally {
            context.unregisterReceiver(receiver);
        }
    }

    private void begin(String operation) {
        synchronized (lock) {
            waiting = operation;
            outcome = null;
        }
    }

    // await waits for operation to end and returns its GATT status, or null after timeout ms.
    private Integer await(String operation, long timeout) throws InterruptedException {
        long end = System.currentTimeMillis() + timeout;
        synchronized (lock) {
            while (outcome == null && operation.equals(waiting)) {
                long left = end - System.currentTimeMillis();
                if (left <= 0) break;
                lock.wait(left);
            }
            Integer result = outcome;
            waiting = null;
            return result;
        }
    }

    private void complete(String operation, int status) {
        synchronized (lock) {
            if (operation.equals(waiting)) {
                outcome = status;
                lock.notifyAll();
            }
        }
    }

    @SuppressWarnings("deprecation") // the calls before Android 13
    private void subscribe(BluetoothGattCharacteristic characteristic) throws IOException, InterruptedException {
        if (!gatt.setCharacteristicNotification(characteristic, true)) throw new RecorderException("failed", "Couldn't subscribe");
        BluetoothGattDescriptor descriptor = characteristic.getDescriptor(CCCD);
        if (descriptor == null) return;
        byte[] value = BluetoothGattDescriptor.ENABLE_NOTIFICATION_VALUE;
        begin("descriptor");
        boolean started;
        if (Build.VERSION.SDK_INT >= 33) {
            started = gatt.writeDescriptor(descriptor, value) == BluetoothStatusCodes.SUCCESS;
        } else {
            descriptor.setValue(value);
            started = gatt.writeDescriptor(descriptor);
        }
        if (!started) throw new RecorderException("failed", "Couldn't subscribe");
        Integer status = await("descriptor", OP_TIMEOUT);
        if (status == null || status != BluetoothGatt.GATT_SUCCESS) throw new RecorderException("failed", "Couldn't subscribe");
    }

    @SuppressWarnings("deprecation") // the calls before Android 13
    private void write(byte[] bytes) throws IOException, InterruptedException {
        if (!connected || gatt == null) throw new RecorderException("disconnected", "The recorder disconnected");
        // The stack may still be busy with the operation before: try a few times.
        for (int attempt = 0; ; attempt++) {
            begin("write");
            boolean started;
            if (Build.VERSION.SDK_INT >= 33) {
                started = gatt.writeCharacteristic(control, bytes, BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT) == BluetoothStatusCodes.SUCCESS;
            } else {
                control.setWriteType(BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT);
                control.setValue(bytes);
                started = gatt.writeCharacteristic(control);
            }
            if (started) {
                Integer status = await("write", OP_TIMEOUT);
                if (status == null) throw new RecorderException("timeout", "Writing took too long");
                if (status == BluetoothGatt.GATT_INSUFFICIENT_AUTHENTICATION || status == BluetoothGatt.GATT_INSUFFICIENT_ENCRYPTION) {
                    throw new RecorderException("auth", "The recorder wants a paired phone");
                }
                if (status != BluetoothGatt.GATT_SUCCESS) throw new RecorderException("failed", "Writing failed (status " + status + ")");
                return;
            }
            if (attempt >= 10 || !connected) throw new RecorderException("failed", "Couldn't write to the recorder");
            Thread.sleep(50);
        }
    }

    @Override
    public synchronized JSONObject request(JSONObject message, long timeout) throws IOException, InterruptedException {
        synchronized (lock) {
            fragments.reset();
            response = null;
        }
        write(message.toString().getBytes(StandardCharsets.UTF_8));
        long end = System.currentTimeMillis() + timeout;
        synchronized (lock) {
            while (response == null) {
                if (!connected) throw new RecorderException("disconnected", "The recorder disconnected");
                long left = end - System.currentTimeMillis();
                if (left <= 0) throw new RecorderException("timeout", "No answer to " + message.optString("op"));
                lock.wait(left);
            }
            JSONObject r = response;
            response = null;
            return r;
        }
    }

    @Override
    public synchronized byte[] read(String id, long offset, int length) throws IOException, InterruptedException {
        ByteArrayOutputStream out = new ByteArrayOutputStream(length);
        synchronized (lock) {
            collected = out;
            next = offset;
            gap = false;
            packets = 0;
            firstAt = -1;
        }
        try {
            JSONObject message = new JSONObject().put("op", "read").put("id", id).put("offset", offset).put("length", length);
            JSONObject r = request(message, READ_TIMEOUT);
            if (!r.optBoolean("ok")) throw new RecorderException("recorder", "read: " + r.optString("message", r.optString("error")));
            synchronized (lock) {
                Log.i(TAG, "recorder read " + id + " from " + offset + ": recorder sent " + r.optLong("length")
                        + ", got " + out.size() + " in " + packets + " packets, first at " + firstAt + (gap ? ", gap" : "")
                        + ", " + late + " late before");
                return out.toByteArray();
            }
        } catch (JSONException e) {
            throw new IOException(e);
        } finally {
            synchronized (lock) {
                collected = null;
            }
        }
    }

    public void close() {
        synchronized (lock) {
            if (late > 0) Log.i(TAG, "recorder: " + late + " data packets came when no read was waiting");
        }
        connected = false;
        BluetoothGatt current = gatt;
        gatt = null;
        if (current == null) return;
        try {
            current.disconnect();
            current.close();
        } catch (SecurityException e) {
            // permission withdrawn meanwhile
        }
        synchronized (lock) {
            lock.notifyAll();
        }
    }

    private void received(BluetoothGattCharacteristic characteristic, byte[] value) {
        if (value == null || value.length == 0) return;
        synchronized (lock) {
            if (CONTROL.equals(characteristic.getUuid())) {
                fragments.write(value, 1, value.length - 1);
                if ((value[0] & 1) != 0) return;
                try {
                    response = new JSONObject(new String(fragments.toByteArray(), StandardCharsets.UTF_8));
                } catch (JSONException e) {
                    // garbled: the request times out
                }
                fragments.reset();
                lock.notifyAll();
            } else if (DATA.equals(characteristic.getUuid()) && collected == null) {
                late++;
            } else if (DATA.equals(characteristic.getUuid()) && collected != null && value.length >= 4) {
                long at = (value[0] & 0xffL) | (value[1] & 0xffL) << 8 | (value[2] & 0xffL) << 16 | (value[3] & 0xffL) << 24;
                if (packets++ == 0) firstAt = at;
                // A gap means a notification got lost: keep what came before it; the relay asks again.
                if (at != next) gap = true;
                if (gap) return;
                collected.write(value, 4, value.length - 4);
                next += value.length - 4;
            }
        }
    }

    private final BluetoothGattCallback callback = new BluetoothGattCallback() {
        @Override
        public void onConnectionStateChange(BluetoothGatt g, int status, int newState) {
            if (newState == BluetoothProfile.STATE_CONNECTED && status == BluetoothGatt.GATT_SUCCESS) {
                connected = true;
                complete("connect", status);
            } else if (newState == BluetoothProfile.STATE_DISCONNECTED) {
                connected = false;
                synchronized (lock) {
                    // Whatever was waited for won't come.
                    if (waiting != null) outcome = status == BluetoothGatt.GATT_SUCCESS ? BluetoothGatt.GATT_FAILURE : status;
                    lock.notifyAll();
                }
            }
        }

        @Override
        public void onMtuChanged(BluetoothGatt g, int mtu, int status) {
            complete("mtu", status);
        }

        @Override
        public void onServicesDiscovered(BluetoothGatt g, int status) {
            complete("services", status);
        }

        @Override
        public void onDescriptorWrite(BluetoothGatt g, BluetoothGattDescriptor descriptor, int status) {
            complete("descriptor", status);
        }

        @Override
        public void onCharacteristicWrite(BluetoothGatt g, BluetoothGattCharacteristic characteristic, int status) {
            complete("write", status);
        }

        @Override
        public void onCharacteristicChanged(@NonNull BluetoothGatt g, @NonNull BluetoothGattCharacteristic characteristic, @NonNull byte[] value) {
            received(characteristic, value);
        }

        @Override
        @SuppressWarnings("deprecation")
        public void onCharacteristicChanged(BluetoothGatt g, BluetoothGattCharacteristic characteristic) {
            // Android 12 and older; newer ones call the one above. The value is reused: copy it.
            if (Build.VERSION.SDK_INT < 33) {
                byte[] value = characteristic.getValue();
                received(characteristic, value == null ? null : value.clone());
            }
        }
    };
}
