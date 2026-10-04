package net.kleinhenz.knowpod.pocket;

import static org.junit.Assert.assertArrayEquals;
import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;
import static org.junit.Assert.fail;

import java.io.File;
import java.io.IOException;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import org.junit.After;
import org.junit.Before;
import org.junit.Test;

// Tests for the WiFi transfer (WifiSession, Transfer) against the fake recorder: the same
// cases as desktop/test/pocket-wifi.test.js.
public class WifiSessionTest {
    private File dir;
    private FakeRecorder recorder;

    @Before
    public void setUp() throws IOException {
        dir = Files.createTempDirectory("pocket-wifi-test-").toFile();
        recorder = new FakeRecorder().withDefaultFiles();
    }

    @After
    public void tearDown() throws IOException {
        recorder.stop();
        File[] files = dir.listFiles();
        if (files != null) for (File f : files) f.delete();
        dir.delete();
    }

    private static PocketSession.Recording recording(String timestamp) {
        return new PocketSession.Recording(FakeRecorder.day(timestamp), timestamp, 0);
    }

    @Test
    public void threeFilesRestartTheAccessPointAfterTwo() throws Exception {
        recorder.marker = false;
        WifiSession session = recorder.wifiSession();
        session.start();
        List<WifiSession.Result> results = new ArrayList<>();
        for (String ts : recorder.files.keySet()) {
            results.add(session.download(recording(ts), new File(dir, ts + ".mp3"), null));
        }
        session.close();

        int i = 0;
        for (String ts : recorder.files.keySet()) {
            assertArrayEquals(ts + " arrived intact", recorder.files.get(ts), Files.readAllBytes(new File(dir, ts + ".mp3").toPath()));
            assertEquals(recorder.files.get(ts).length, results.get(i).size);
            assertFalse(results.get(i).markerOk);
            i++;
        }
        assertEquals(2, recorder.apStarts);
        assertEquals(Arrays.asList(), recorder.violations);
        assertEquals(
                Arrays.asList("setup", "subscribeAudio", "prepare PKT01_GREY_TEST abcd1234", "join", "restart", "leave", "join", "restore"),
                recorder.calls);
        assertFalse("no partial files left", new File(dir, "20261003142550.mp3.part").exists());
    }

    @Test
    public void theAppsOrderWifioWifiTheRequestThenTheSwitch() throws Exception {
        WifiSession session = recorder.wifiSession();
        session.start();
        WifiSession.Result result = session.download(recording("20261003160116"), new File(dir, "a.mp3"), null);
        session.close();
        assertTrue(result.markerOk);
        List<String> commands = new ArrayList<>();
        for (String name : recorder.sent) if (!name.equals("WIFIS") && !name.equals("WPING")) commands.add(name);
        assertEquals(Arrays.asList("WIFIO", "WIFI", "U&2026-10-03&20261003160116", "U&WIFI", "WIFIC"), commands);
    }

    @Test
    public void aResetAfterTheFileKeepsTheFile() throws Exception {
        recorder.resetAfterFile = true;
        WifiSession session = recorder.wifiSession();
        session.start();
        for (String ts : recorder.files.keySet()) session.download(recording(ts), new File(dir, ts + ".mp3"), null);
        session.close();
        for (String ts : recorder.files.keySet()) {
            assertArrayEquals(recorder.files.get(ts), Files.readAllBytes(new File(dir, ts + ".mp3").toPath()));
        }
        assertEquals(Arrays.asList(), recorder.violations);
    }

    @Test
    public void aConnectionResetRightAfterItWasAcceptedIsNeverSwitchedTo() throws Exception {
        recorder.resetAccepted.add(1);
        WifiSession session = recorder.wifiSession();
        session.start();
        try {
            session.download(recording("20261003142550"), new File(dir, "a.mp3"), null);
            fail("the dropped connection should fail the file");
        } catch (PocketException e) {
            assertEquals("transfer", e.code);
        }
        // The next file starts on a fresh access point and arrives.
        session.download(recording("20261003141332"), new File(dir, "b.mp3"), null);
        session.close();
        assertArrayEquals(recorder.files.get("20261003141332"), Files.readAllBytes(new File(dir, "b.mp3").toPath()));
        assertEquals(Arrays.asList(), recorder.violations);
        assertEquals(2, recorder.apStarts);
        assertFalse("the file was never asked for", recorder.sent.contains("U&2026-10-03&20261003142550"));
    }

    @Test
    public void aRecordingTheRecorderRefusesFailsAloneWithItsAnswer() throws Exception {
        recorder.answers.put("20261003142550", "ERR");
        WifiSession session = recorder.wifiSession();
        session.start();
        try {
            session.download(recording("20261003142550"), new File(dir, "a.mp3"), null);
            fail("a refused recording should fail");
        } catch (PocketException e) {
            assertEquals("refused", e.code);
            assertTrue(e.getMessage(), e.getMessage().contains("MCU&U&ERR"));
        }
        session.download(recording("20261003141332"), new File(dir, "b.mp3"), null);
        session.close();
        assertArrayEquals(recorder.files.get("20261003141332"), Files.readAllBytes(new File(dir, "b.mp3").toPath()));
        assertEquals(Arrays.asList(), recorder.violations);
    }

    @Test
    public void aRecorderThatStopsAnsweringWifioIsStuck() throws Exception {
        recorder.wifioAnswers = 1;
        WifiSession session = recorder.wifiSession();
        session.start();
        session.download(recording("20261003142550"), new File(dir, "a.mp3"), null);
        session.download(recording("20261003141332"), new File(dir, "b.mp3"), null);
        try {
            session.download(recording("20261003160116"), new File(dir, "c.mp3"), null);
            fail("the third file needs a restart the recorder doesn't answer");
        } catch (PocketException e) {
            assertEquals("stuck", e.code);
        } finally {
            session.close();
        }
    }

    @Test
    public void cancelStopsATransfer() throws Exception {
        WifiSession session = recorder.wifiSession();
        session.start();
        session.cancel();
        try {
            session.download(recording("20261003142550"), new File(dir, "a.mp3"), null);
            fail("cancelled");
        } catch (PocketException e) {
            assertEquals("cancelled", e.code);
        } finally {
            session.close();
        }
        assertTrue(recorder.sent.contains("WIFIC"));
        assertTrue(recorder.calls.contains("restore"));
    }
}
