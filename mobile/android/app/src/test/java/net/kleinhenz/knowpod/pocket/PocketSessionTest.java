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
    public void aRefusedKeyIsAuth() throws Exception {
        MessageLog log = new MessageLog();
        PocketSession session = new PocketSession(new PocketSession.Link() {
            @Override
            public void write(byte[] bytes) {
                log.add("MCU&SK&ERR");
            }

            @Override
            public void subscribeAudio() { }

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
                public void subscribeAudio() { }

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
            public void subscribeAudio() { }

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
