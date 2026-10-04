package net.kleinhenz.knowpod.pocket;

import static org.junit.Assert.assertArrayEquals;
import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import java.io.File;
import java.io.IOException;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import org.junit.After;
import org.junit.Before;
import org.junit.Test;

// Tests for the whole copy (WifiSync): list, ask the server, transfer over Bluetooth or WiFi,
// upload.
public class WifiSyncTest {
    private File temp;
    private FakeRecorder recorder;
    private final Map<String, byte[]> uploaded = Collections.synchronizedMap(new HashMap<>());
    private final Set<String> remembered = Collections.synchronizedSet(new LinkedHashSet<>());
    private final List<String> finished = new ArrayList<>();
    // the server's notes: these files are already notes
    private final Set<String> notes = new LinkedHashSet<>();
    private int uploadStatus = 0; // 0: upload works; else the HTTP status the server answers
    private boolean noWifi; // the phone can't join the recorder's network
    private static final long BLUETOOTH_MAX_BYTES = WifiSync.bluetoothMaxBytes;

    @Before
    public void setUp() throws IOException {
        temp = new File(Files.createTempDirectory("pocket-sync-test-").toFile(), "copy");
        recorder = new FakeRecorder().withDefaultFiles();
    }

    @After
    public void tearDown() throws IOException {
        recorder.stop();
        temp.getParentFile().delete();
        WifiSync.bluetoothMaxBytes = BLUETOOTH_MAX_BYTES;
    }

    private final ServerApi api = new ServerApi(url -> null) {
        @Override
        public List<String> newFiles(String server, List<String> names) {
            List<String> fresh = new ArrayList<>();
            for (String n : names) if (!notes.contains(n)) fresh.add(n);
            return fresh;
        }

        @Override
        public String upload(String server, File file, String name) throws IOException {
            if (uploadStatus != 0) throw new ServerApi.HttpException(uploadStatus, "{\"message\":\"boom\"}");
            uploaded.put(name, Files.readAllBytes(file.toPath()));
            return "note-" + name;
        }
    };

    private WifiSync sync() {
        WifiSync.Env env = new WifiSync.Env() {
            @Override
            public String serverUrl() {
                return "https://knowpod.example.com";
            }

            @Override
            public PocketSession openSession() throws PocketException, InterruptedException {
                PocketSession session = recorder.session();
                session.unlock("0123456789abcdef");
                return session;
            }

            @Override
            public HostWifi wifi() {
                return noWifi ? null : recorder.wifi();
            }

            @Override
            public ServerApi api() {
                return api;
            }

            @Override
            public Set<String> remembered(String server) {
                return new LinkedHashSet<>(remembered);
            }

            @Override
            public void remember(String server, String name) {
                remembered.add(name);
            }

            @Override
            public File tempDir() {
                return temp;
            }

            @Override
            public void log(String text) { }

            @Override
            public void changed() { }

            @Override
            public void finished(String lastId) {
                finished.add(lastId);
            }
        };
        return new WifiSync(env, "127.0.0.1", recorder.port, FakeRecorder.timings(), FakeRecorder.bluetoothTimings());
    }

    @Test
    public void copiesTheNewRecordings() throws Exception {
        WifiSync.bluetoothMaxBytes = 0; // all over WiFi
        notes.add("20261003141332.mp3"); // already a note
        WifiSync sync = sync();
        assertTrue(sync.start());
        assertFalse("one copy at a time", sync.start());
        sync.join();

        Map<String, Object> state = sync.state();
        assertEquals(state.toString(), "done", state.get("phase"));
        assertEquals(2, state.get("copied"));
        assertEquals(0, state.get("failed"));
        assertEquals(3, state.get("found"));
        assertEquals(false, state.get("running"));
        assertArrayEquals(recorder.files.get("20261003142550"), uploaded.get("20261003142550.mp3"));
        assertArrayEquals(recorder.files.get("20261003160116"), uploaded.get("20261003160116.mp3"));
        assertEquals(3, remembered.size());
        assertEquals(Collections.singletonList("note-20261003160116.mp3"), finished);
        assertFalse("the temporary folder is gone", temp.exists());
        assertEquals(Collections.emptyList(), recorder.violations);
    }

    @Test
    public void nothingNewNeedsNoWifi() throws Exception {
        remembered.add("20261003142550.mp3");
        notes.add("20261003141332.mp3");
        notes.add("20261003160116.mp3");
        WifiSync sync = sync();
        sync.start();
        sync.join();
        assertEquals("done", sync.state().get("phase"));
        assertEquals(0, sync.state().get("copied"));
        assertEquals(0, recorder.apStarts);
        assertFalse(recorder.sent.contains("WIFIO"));
    }

    @Test
    public void aFailedUploadFailsTheCopy() throws Exception {
        WifiSync.bluetoothMaxBytes = 0;
        uploadStatus = 500;
        WifiSync sync = sync();
        sync.start();
        sync.join();
        Map<String, Object> state = sync.state();
        assertEquals("failed", state.get("phase"));
        assertEquals("server", state.get("error"));
        assertEquals("boom", state.get("message"));
        assertTrue("the recorder's WiFi was lowered", recorder.sent.contains("WIFIC"));
    }

    private void assertAllUploaded() {
        for (Map.Entry<String, byte[]> f : recorder.files.entrySet()) {
            assertArrayEquals(f.getKey(), f.getValue(), uploaded.get(f.getKey() + ".mp3"));
        }
    }

    @Test
    public void shortRecordingsComeOverBluetoothWithoutWifi() throws Exception {
        WifiSync sync = sync();
        sync.start();
        sync.join();
        Map<String, Object> state = sync.state();
        assertEquals(state.toString(), "done", state.get("phase"));
        assertEquals(3, state.get("copied"));
        assertEquals(0, state.get("failed"));
        assertAllUploaded();
        assertEquals(3, recorder.overBluetooth.size());
        assertFalse("no access point for short recordings", recorder.sent.contains("WIFIO"));
        assertEquals(Collections.emptyList(), recorder.violations);
    }

    @Test
    public void shortOverBluetoothLongOverWifi() throws Exception {
        WifiSync.bluetoothMaxBytes = 500_000; // the 92 KB recording over Bluetooth
        WifiSync sync = sync();
        sync.start();
        sync.join();
        Map<String, Object> state = sync.state();
        assertEquals(state.toString(), "done", state.get("phase"));
        assertEquals(3, state.get("copied"));
        assertAllUploaded();
        assertEquals(Collections.singletonList("20261003160116"), recorder.overBluetooth);
        // Bluetooth first, then the access point for the long ones.
        int bluetooth = recorder.sent.indexOf("U&2026-10-03&20261003160116");
        int wifi = recorder.sent.indexOf("WIFIO");
        assertTrue(recorder.sent.toString(), bluetooth >= 0 && wifi > bluetooth);
        assertEquals(1, recorder.apStarts);
        assertTrue(recorder.sent.contains("WIFIC"));
        assertEquals(Collections.emptyList(), recorder.violations);
    }

    @Test
    public void withoutTheRecordersWifiAllComeOverBluetooth() throws Exception {
        WifiSync.bluetoothMaxBytes = 0;
        recorder.joinable = false;
        WifiSync sync = sync();
        sync.start();
        sync.join();
        Map<String, Object> state = sync.state();
        assertEquals(state.toString(), "done", state.get("phase"));
        assertEquals(3, state.get("copied"));
        assertAllUploaded();
        assertEquals(3, recorder.overBluetooth.size());
        assertTrue("the access point was lowered", recorder.sent.contains("WIFIC"));
        assertTrue(recorder.calls.contains("restore"));
        assertEquals(Collections.emptyList(), recorder.violations);
    }

    @Test
    public void aPhoneWithoutWifiCopiesOverBluetooth() throws Exception {
        WifiSync.bluetoothMaxBytes = 0;
        noWifi = true;
        WifiSync sync = sync();
        sync.start();
        sync.join();
        assertEquals("done", sync.state().get("phase"));
        assertEquals(3, sync.state().get("copied"));
        assertAllUploaded();
        assertFalse(recorder.sent.contains("WIFIO"));
    }

    @Test
    public void aBluetoothTransferThatStopsShortFailsThatRecording() throws Exception {
        recorder.bluetoothStopAt = 50_000; // all three are longer
        WifiSync sync = sync();
        sync.start();
        sync.join();
        Map<String, Object> state = sync.state();
        assertEquals(state.toString(), "done", state.get("phase"));
        assertEquals(0, state.get("copied"));
        assertEquals(3, state.get("failed"));
        assertEquals("transfer", state.get("error"));
        assertTrue(uploaded.isEmpty());
        assertFalse("not remembered: copying again picks them up", remembered.contains("20261003160116.mp3"));
    }
}
