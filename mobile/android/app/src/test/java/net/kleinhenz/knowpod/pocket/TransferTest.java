package net.kleinhenz.knowpod.pocket;

import static org.junit.Assert.assertArrayEquals;
import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;
import static org.junit.Assert.fail;

import java.io.File;
import java.io.IOException;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.file.Files;
import java.util.Arrays;
import org.junit.After;
import org.junit.Before;
import org.junit.Test;

// Tests for receiving one file (Transfer.receive), as receiveFile in
// desktop/test/pocket-wifi.test.js.
public class TransferTest {
    private ServerSocket server;
    private File dir;

    @Before
    public void setUp() throws IOException {
        server = new ServerSocket(0, 50, InetAddress.getLoopbackAddress());
        dir = Files.createTempDirectory("pocket-transfer-test-").toFile();
    }

    @After
    public void tearDown() throws IOException {
        server.close();
        File[] files = dir.listFiles();
        if (files != null) for (File f : files) f.delete();
        dir.delete();
    }

    private interface Sender {
        void send(OutputStream out, Socket socket) throws Exception;
    }

    // connect gives a Transfer whose other end sends what sender writes, from another thread.
    private Transfer connect(Sender sender) throws Exception {
        Thread thread = new Thread(() -> {
            try (Socket socket = server.accept()) {
                sender.send(socket.getOutputStream(), socket);
            } catch (Exception ignored) {
                // the test sees it
            }
        });
        thread.setDaemon(true);
        thread.start();
        return new Transfer(new Socket(InetAddress.getLoopbackAddress(), server.getLocalPort()));
    }

    private static Transfer.Timeouts fast() {
        Transfer.Timeouts t = new Transfer.Timeouts();
        t.firstByte = 500;
        t.idle = 500;
        t.marker = 200;
        return t;
    }

    @Test
    public void splitsTheMarkerOff() throws Exception {
        byte[] file = FakeRecorder.mp3(100_000, 5);
        Transfer transfer = connect((out, socket) -> {
            // In pieces, the marker split across two writes.
            out.write(Arrays.copyOf(file, 40_000));
            out.flush();
            Thread.sleep(20);
            out.write(Arrays.copyOfRange(file, 40_000, file.length));
            out.write(Arrays.copyOf(Transfer.END_MARKER, 3));
            out.flush();
            Thread.sleep(20);
            out.write(Arrays.copyOfRange(Transfer.END_MARKER, 3, 10));
            out.flush();
            Thread.sleep(500);
        });
        File target = new File(dir, "f.mp3");
        long[] last = {0};
        assertTrue(transfer.receive(file.length, target, fast(), (received, size) -> last[0] = received));
        assertArrayEquals(file, Files.readAllBytes(target.toPath()));
        assertEquals(file.length, last[0]);
        transfer.close(100);
    }

    @Test
    public void aShortTransferFailsAndLeavesNothing() throws Exception {
        byte[] file = FakeRecorder.mp3(1_000, 1);
        Transfer transfer = connect((out, socket) -> out.write(file, 0, 500));
        File target = new File(dir, "f.mp3");
        try {
            transfer.receive(file.length, target, fast(), null);
            fail("short");
        } catch (PocketException e) {
            assertEquals("transfer", e.code);
            assertTrue(e.getMessage(), e.getMessage().contains("500 of 1000"));
        }
        assertFalse(target.exists());
        assertFalse(new File(dir, "f.mp3.part").exists());
    }

    @Test
    public void timesOutWhenNothingArrives() throws Exception {
        Transfer transfer = connect((out, socket) -> Thread.sleep(1_500));
        try {
            transfer.receive(1_000, new File(dir, "f.mp3"), fast(), null);
            fail("nothing came");
        } catch (PocketException e) {
            assertEquals("The Pocket sent nothing", e.getMessage());
        }
    }

    @Test
    public void aMissingMarkerKeepsTheCompleteFile() throws Exception {
        byte[] file = FakeRecorder.mp3(2_000, 2);
        Transfer transfer = connect((out, socket) -> {
            out.write(file);
            out.flush();
            Thread.sleep(1_000);
        });
        File target = new File(dir, "f.mp3");
        assertFalse(transfer.receive(file.length, target, fast(), null));
        assertArrayEquals(file, Files.readAllBytes(target.toPath()));
    }

    @Test
    public void aliveSeesAClosedConnection() throws Exception {
        Transfer transfer = connect((out, socket) -> { });
        Thread.sleep(100);
        assertFalse(transfer.alive());
    }

    @Test
    public void aliveKeepsAByteThatCame() throws Exception {
        byte[] file = {1, 2, 3};
        Transfer transfer = connect((out, socket) -> {
            out.write(file);
            out.write(Transfer.END_MARKER);
            out.flush();
            Thread.sleep(500);
        });
        Thread.sleep(100);
        assertTrue(transfer.alive());
        File target = new File(dir, "f.mp3");
        assertTrue(transfer.receive(file.length, target, fast(), null));
        assertArrayEquals(file, Files.readAllBytes(target.toPath()));
    }
}
