package net.kleinhenz.knowpod.pocket;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;
import static org.junit.Assert.fail;

import java.io.IOException;
import java.util.Arrays;
import java.util.Map;
import org.junit.Test;

public class PocketSessionTest {
    @Test
    public void splitsNotificationsWithSeveralAnswers() {
        assertEquals(Arrays.asList("MCU&WIFIO", "MCU&OFF"), MessageLog.split("MCU&WIFIOMCU&OFF\0"));
        assertEquals("", MessageLog.valueOf("MCU&WIFIO", "WIFIO"));
        assertEquals("1", MessageLog.valueOf("MCU&WIFIS&1", "WIFIS"));
        assertNull(MessageLog.valueOf("MCU&WIFIS&1", "WIFI"));
    }

    @Test
    public void unlocksAndChecks() throws Exception {
        FakeRecorder recorder = new FakeRecorder();
        PocketSession session = recorder.session();
        session.unlock("0123456789abcdef");
        Map<String, Object> info = session.check();
        assertEquals(58, info.get("battery"));
        assertEquals("1.8", info.get("firmware"));
        assertEquals("SK&0123456789abcdef", recorder.sent.get(0));
    }

    @Test
    public void switchesTheUsbDriveOn() throws Exception {
        FakeRecorder recorder = new FakeRecorder();
        PocketSession session = recorder.session();
        session.unlock("0123456789abcdef");
        assertEquals(Boolean.FALSE, session.check().get("usb"));
        session.usbOn();
        assertTrue(recorder.sent.contains("USB&1"));
        assertEquals(Boolean.TRUE, session.check().get("usb"));
    }

    @Test
    public void aUsbDriveThatStaysOffIsRefused() throws Exception {
        FakeRecorder recorder = new FakeRecorder();
        recorder.usbSwitches = false;
        PocketSession session = recorder.session();
        session.unlock("0123456789abcdef");
        try {
            session.usbOn();
            fail("refused");
        } catch (PocketException e) {
            assertEquals("usb-refused", e.code);
        }
    }

    @Test
    public void aRefusedKeyIsAuth() throws Exception {
        MessageLog log = new MessageLog();
        PocketSession session = new PocketSession(new PocketSession.Link() {
            @Override
            public void write(byte[] bytes) {
                log.add("MCU&SK&ERR");
            }

            @Override
            public void subscribeAudio(java.util.function.Consumer<byte[]> listener) { }

            @Override
            public void close() { }
        }, log);
        try {
            session.unlock("wrong-key");
            fail("refused");
        } catch (PocketException e) {
            assertEquals("auth", e.code);
        }
    }

    // activationLink answers APP&SK&<key> like a recorder: answer, then (after "OK") drops the
    // connection, or (failing) hangs up before acknowledging the write.
    private static PocketSession.Link activationLink(MessageLog log, String answer, boolean failWrite, boolean[] pocketAppWrites) {
        return new PocketSession.Link() {
            @Override
            public void write(byte[] bytes) throws IOException {
                if (answer != null) log.add("MCU&SK&" + answer);
                if ("OK".equals(answer)) log.add("MCU&WIFIO");
                if (failWrite || "OK".equals(answer)) log.disconnect();
                if (failWrite) throw new IOException("GATT error");
            }

            @Override
            public void usePocketAppWrites() {
                pocketAppWrites[0] = true;
            }

            @Override
            public void subscribeAudio(java.util.function.Consumer<byte[]> listener) { }

            @Override
            public void close() { }
        };
    }

    @Test
    public void activatesWithThePocketAppsWrites() throws Exception {
        MessageLog log = new MessageLog();
        boolean[] appWrites = {false};
        assertEquals("OK", new PocketSession(activationLink(log, "OK", false, appWrites), log).activate("ABCDEFGH12345678"));
        assertTrue(appWrites[0]);
    }

    @Test
    public void aRefusalCountsThoughTheWriteFails() throws Exception {
        MessageLog log = new MessageLog();
        assertEquals("ERR", new PocketSession(activationLink(log, "ERR", true, new boolean[1]), log).activate("ABCDEFGH12345678"));
    }

    @Test
    public void generatesSessionKeysLikeThePocketApp() {
        String key = PocketController.generateSessionKey();
        assertTrue(key, key.matches("[A-Z0-9]{16}"));
        assertTrue(PocketSettings.validSessionKey(key));
    }

    @Test
    public void listsRecordingsOnceThoughNotificationsRepeat() throws Exception {
        FakeRecorder recorder = new FakeRecorder().withDefaultFiles();
        recorder.files.put("20261002090356", FakeRecorder.mp3(8_000, 4));
        recorder.repeats = 4;
        PocketSession.Listing listing = recorder.session().listRecordings();
        assertEquals(4, listing.recordings.size());
        assertEquals(2, listing.days);
        assertFalse(listing.incomplete);
        assertEquals("20261002090356", listing.recordings.get(0).timestamp);
        assertEquals("2026-10-02", listing.recordings.get(0).date);
        assertEquals("20261003160116.mp3", listing.recordings.get(3).name());
    }

    @Test
    public void aShortListingIsIncomplete() throws Exception {
        PocketSession.listSettle = 1;
        PocketSession.listTimeout = 200;
        try {
            MessageLog log = new MessageLog();
            PocketSession session = new PocketSession(new PocketSession.Link() {
                @Override
                public void write(byte[] bytes) {
                    String name = new String(bytes).substring(4);
                    if (name.equals("LIST_DIRS")) {
                        log.add("MCU&DIRS&2026-10-03");
                        log.add("MCU&DIRS_SUM&1");
                    } else {
                        // Counts two, sends one.
                        log.add("MCU&F&2026-10-03&20261003142550&221");
                        log.add("MCU&LIST&2");
                    }
                }

                @Override
                public void subscribeAudio(java.util.function.Consumer<byte[]> listener) { }

                @Override
                public void close() { }
            }, log);
            PocketSession.Listing listing = session.listRecordings();
            assertEquals(1, listing.recordings.size());
            assertTrue(listing.incomplete);
        } finally {
            PocketSession.listSettle = 250;
            PocketSession.listTimeout = 10_000;
        }
    }

    @Test
    public void aLostConnectionEndsWaiting() throws Exception {
        MessageLog log = new MessageLog();
        PocketSession session = new PocketSession(new PocketSession.Link() {
            @Override
            public void write(byte[] bytes) throws IOException {
                log.disconnect();
            }

            @Override
            public void subscribeAudio(java.util.function.Consumer<byte[]> listener) { }

            @Override
            public void close() { }
        }, log);
        try {
            session.command("BAT", "BAT");
            fail("disconnected");
        } catch (PocketException e) {
            assertEquals("disconnected", e.code);
        }
        assertFalse(session.connected());
    }
}
