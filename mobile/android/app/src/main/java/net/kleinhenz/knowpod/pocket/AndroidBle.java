package net.kleinhenz.knowpod.pocket;

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
import android.content.Context;
import android.os.Build;
import android.util.Log;
import androidx.annotation.NonNull;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.Collections;
import java.util.UUID;

// AndroidBle is the Bluetooth LE connection to a Pocket recorder on the phone: it finds the
// recorder by its address, connects, and carries the commands of a PocketSession (the
// counterpart of the Web Bluetooth part of desktop/src/bluetooth.js). Android runs one GATT
// operation at a time, so writes and subscriptions wait for the one before.
@SuppressLint("MissingPermission") // PocketController asks for the permissions first
public final class AndroidBle implements PocketSession.Link {
    private static final String TAG = "knowpod";
    static final UUID SERVICE = UUID.fromString("001120a0-2233-4455-6677-889912345678");
    static final UUID COMMAND = UUID.fromString("001120a3-2233-4455-6677-889912345678");
    // The recording data of a Bluetooth transfer (see PocketSession.Link.subscribeAudio).
    static final UUID AUDIO = UUID.fromString("001120a1-2233-4455-6677-889912345678");
    private static final UUID CCCD = UUID.fromString("00002902-0000-1000-8000-00805f9b34fb");

    // How long to look for the recorder before giving up (it may be asleep).
    private static final long SCAN_TIMEOUT = 20_000;
    private static final long CONNECT_TIMEOUT = 20_000;
    private static final long OP_TIMEOUT = 5_000;

    private final Context context;
    private final MessageLog log = new MessageLog();
    private final Object lock = new Object();
    // The GATT operation waited for and its outcome (a GATT status), or null while it runs.
    private String waiting;
    private Integer outcome;
    private volatile boolean connected;
    private BluetoothGatt gatt;
    private BluetoothGattCharacteristic command;
    private BluetoothGattCharacteristic audio;

    private AndroidBle(Context context) {
        this.context = context.getApplicationContext();
    }

    // connect finds the recorder at address, connects and subscribes to its answers. The
    // session still has to be unlocked (PocketSession.unlock).
    public static PocketSession connect(Context context, String address) throws PocketException, InterruptedException {
        AndroidBle ble = new AndroidBle(context);
        try {
            ble.open(address);
        } catch (SecurityException e) {
            ble.close();
            throw new PocketException("permission", e.getMessage(), e);
        } catch (PocketException | InterruptedException | RuntimeException e) {
            ble.close();
            throw e;
        }
        return new PocketSession(ble, ble.log);
    }

    private void open(String address) throws PocketException, InterruptedException {
        BluetoothManager manager = (BluetoothManager) context.getSystemService(Context.BLUETOOTH_SERVICE);
        BluetoothAdapter adapter = manager == null ? null : manager.getAdapter();
        if (adapter == null) throw new PocketException("unsupported", "This phone has no Bluetooth");
        if (!adapter.isEnabled()) throw new PocketException("unsupported", "Bluetooth is off");
        BluetoothDevice device = scan(adapter, address);

        // A first connection sometimes fails at once (status 133); a second try usually works.
        for (int attempt = 1; ; attempt++) {
            begin("connect");
            gatt = device.connectGatt(context, false, callback, BluetoothDevice.TRANSPORT_LE);
            if (gatt == null) throw new PocketException("failed", "Couldn't connect");
            Integer status = await("connect", CONNECT_TIMEOUT);
            if (status != null && status == BluetoothGatt.GATT_SUCCESS && connected) break;
            gatt.close();
            gatt = null;
            if (attempt >= 2) {
                throw status == null ? new PocketException("timeout", "Connecting took too long")
                        : new PocketException("failed", "Connecting failed (status " + status + ")");
            }
            Thread.sleep(500);
        }
        // Commands like "APP&SK&<16 characters>" don't fit the default 20 bytes.
        begin("mtu");
        if (gatt.requestMtu(247)) await("mtu", OP_TIMEOUT);
        begin("services");
        if (!gatt.discoverServices()) throw new PocketException("failed", "Couldn't read the services");
        Integer discovered = await("services", 15_000);
        if (discovered == null || discovered != BluetoothGatt.GATT_SUCCESS) throw new PocketException("failed", "Couldn't read the services");
        BluetoothGattService service = gatt.getService(SERVICE);
        command = service == null ? null : service.getCharacteristic(COMMAND);
        audio = service == null ? null : service.getCharacteristic(AUDIO);
        if (command == null) throw new PocketException("failed", "This device isn't a Pocket");
        subscribe(command);
    }

    // scan finds the recorder: it answers only while it advertises (awake, and not connected
    // to another phone, e.g. the Pocket app).
    private BluetoothDevice scan(BluetoothAdapter adapter, String address) throws PocketException, InterruptedException {
        BluetoothLeScanner scanner = adapter.getBluetoothLeScanner();
        if (scanner == null) throw new PocketException("unsupported", "Bluetooth is off");
        final BluetoothDevice[] found = new BluetoothDevice[1];
        final int[] failed = {0};
        ScanCallback scanCallback = new ScanCallback() {
            @Override
            public void onScanResult(int callbackType, ScanResult result) {
                synchronized (found) {
                    found[0] = result.getDevice();
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
        ScanFilter filter = new ScanFilter.Builder().setDeviceAddress(address).build();
        ScanSettings settings = new ScanSettings.Builder().setScanMode(ScanSettings.SCAN_MODE_LOW_LATENCY).build();
        scanner.startScan(Collections.singletonList(filter), settings, scanCallback);
        try {
            long end = System.currentTimeMillis() + SCAN_TIMEOUT;
            synchronized (found) {
                while (found[0] == null && failed[0] == 0) {
                    long left = end - System.currentTimeMillis();
                    if (left <= 0) break;
                    found.wait(left);
                }
            }
        } finally {
            try {
                scanner.stopScan(scanCallback);
            } catch (IllegalStateException e) {
                // Bluetooth went off meanwhile
            }
        }
        if (failed[0] != 0) throw new PocketException("failed", "Scanning failed (" + failed[0] + ")");
        if (found[0] == null) throw new PocketException("not-found", "The Pocket wasn't found");
        return found[0];
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

    // subscribe turns on notifications of characteristic.
    @SuppressWarnings("deprecation") // the calls before Android 13
    private void subscribe(BluetoothGattCharacteristic characteristic) throws PocketException, InterruptedException {
        synchronized (this) {
            if (!gatt.setCharacteristicNotification(characteristic, true)) throw new PocketException("failed", "Couldn't subscribe");
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
            if (!started) throw new PocketException("failed", "Couldn't subscribe");
            Integer status = await("descriptor", OP_TIMEOUT);
            if (status == null || status != BluetoothGatt.GATT_SUCCESS) throw new PocketException("failed", "Couldn't subscribe");
        }
    }

    @Override
    @SuppressWarnings("deprecation") // the calls before Android 13
    public synchronized void write(byte[] bytes) throws IOException {
        if (!connected || gatt == null) throw new IOException("The recorder disconnected");
        int type = (command.getProperties() & BluetoothGattCharacteristic.PROPERTY_WRITE_NO_RESPONSE) != 0
                ? BluetoothGattCharacteristic.WRITE_TYPE_NO_RESPONSE
                : BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT;
        try {
            // The stack may still be busy with the operation before: try a few times.
            for (int attempt = 0; ; attempt++) {
                begin("write");
                boolean started;
                if (Build.VERSION.SDK_INT >= 33) {
                    started = gatt.writeCharacteristic(command, bytes, type) == BluetoothStatusCodes.SUCCESS;
                } else {
                    command.setWriteType(type);
                    command.setValue(bytes);
                    started = gatt.writeCharacteristic(command);
                }
                if (started) {
                    Integer status = await("write", OP_TIMEOUT);
                    if (status == null) throw new IOException("Writing took too long");
                    if (status != BluetoothGatt.GATT_SUCCESS) throw new IOException("Writing failed (status " + status + ")");
                    return;
                }
                if (attempt >= 10 || !connected) throw new IOException("Couldn't write to the recorder");
                Thread.sleep(50);
            }
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new IOException("Interrupted");
        } catch (SecurityException e) {
            throw new IOException(e.getMessage());
        }
    }

    @Override
    public void subscribeAudio() throws IOException {
        if (audio == null) throw new IOException("The recorder has no audio characteristic");
        try {
            subscribe(audio);
        } catch (PocketException e) {
            throw new IOException(e.getMessage());
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new IOException("Interrupted");
        }
    }

    @Override
    public void close() {
        connected = false;
        log.disconnect();
        BluetoothGatt current = gatt;
        gatt = null;
        if (current == null) return;
        try {
            current.disconnect();
            current.close();
        } catch (SecurityException e) {
            Log.w(TAG, "pocket bluetooth: " + e.getMessage());
        }
    }

    private void received(BluetoothGattCharacteristic characteristic, byte[] value) {
        if (value == null || !COMMAND.equals(characteristic.getUuid())) return; // audio is thrown away
        log.add(new String(value, StandardCharsets.US_ASCII));
    }

    private final BluetoothGattCallback callback = new BluetoothGattCallback() {
        @Override
        public void onConnectionStateChange(BluetoothGatt g, int status, int newState) {
            if (newState == BluetoothProfile.STATE_CONNECTED && status == BluetoothGatt.GATT_SUCCESS) {
                connected = true;
                complete("connect", status);
            } else if (newState == BluetoothProfile.STATE_DISCONNECTED) {
                boolean was = connected;
                connected = false;
                if (was) log.disconnect();
                synchronized (lock) {
                    // Whatever was waited for won't come.
                    if (waiting != null) {
                        outcome = status == BluetoothGatt.GATT_SUCCESS ? BluetoothGatt.GATT_FAILURE : status;
                        lock.notifyAll();
                    }
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
            // Android 12 and older; newer ones call the one above.
            if (Build.VERSION.SDK_INT < 33) received(characteristic, characteristic.getValue());
        }
    };
}
