import Darwin
import Foundation

// HostWifi moves this device onto the recorder's network and back (desktop: NetworkManager or
// netsh in desktop/src/pocket-wifi.js; Android: AndroidWifi; the iPhone: IOSWifi).
protocol HostWifi: AnyObject {
    // setup checks that this device can join the recorder's network at all ('wifi-setup',
    // 'wifi-unsupported').
    func setup() throws

    // prepare takes the network's name and password (from APP&WIFI).
    func prepare(ssid: String, password: String) throws

    // join joins the network, giving up at deadline (Clock.now()); returns whether this device
    // has an address on it. It must never ask again while a join is under way: the recorder
    // drops its transfer socket when its client leaves.
    func join(ssid: String, deadline: Int64) throws -> Bool

    // leave leaves the network (the recorder's access point is being restarted).
    func leave()

    // restore puts this device back on its usual network. Never throws.
    func restore()

    // connect opens a TCP connection to host:port over the recorder's network and returns its
    // socket.
    func connect(host: String, port: Int, timeout: Int32) throws -> Int32
}

// SocketError is a failed socket call.
struct SocketError: Error, CustomStringConvertible {
    let description: String

    init(_ description: String) {
        self.description = description
    }

    // errno describes the last failed call.
    static func errno(_ code: Int32 = Darwin.errno) -> SocketError {
        SocketError(String(cString: strerror(code)))
    }
}

// Transfer is one connection to the recorder's transfer socket (192.168.200.1:8475), which
// carries one recording: exactly <size> bytes of MP3 file, then a fixed 10-byte end marker.
// The port of mobile/android/…/pocket/Transfer.java, on a plain BSD socket.
final class Transfer {
    static let endMarker: [UInt8] = [0xba, 0x5a, 0x02, 0x8f, 0x04, 0xba, 0x5a, 0x02, 0x8f, 0x04]

    // Timeouts of receive(), ms.
    struct Timeouts {
        var firstByte: Int32 = 15_000
        var idle: Int32 = 15_000
        var marker: Int32 = 3_000
    }

    private let fd: Int32
    private let lock = NSLock()
    private var closed = false
    private var lost: String? // why the connection is gone, once it is

    init(socket: Int32) {
        fd = socket
    }

    deinit {
        if !closed { Darwin.close(fd) }
    }

    // connectSocket opens a TCP connection to host (an IPv4 address):port within timeout ms.
    static func connectSocket(host: String, port: Int, timeout: Int32) throws -> Int32 {
        let fd = Darwin.socket(AF_INET, SOCK_STREAM, IPPROTO_TCP)
        if fd < 0 { throw SocketError.errno() }
        var on: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))
        _ = fcntl(fd, F_SETFL, fcntl(fd, F_GETFL, 0) | O_NONBLOCK)
        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_port = in_port_t(UInt16(port).bigEndian)
        guard inet_pton(AF_INET, host, &address.sin_addr) == 1 else {
            Darwin.close(fd)
            throw SocketError("Not an address: " + host)
        }
        let result = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        if result != 0 {
            let code = Darwin.errno
            if code != EINPROGRESS {
                Darwin.close(fd)
                throw SocketError.errno(code)
            }
            var pfd = pollfd(fd: fd, events: Int16(POLLOUT), revents: 0)
            let ready = Darwin.poll(&pfd, 1, timeout)
            if ready <= 0 {
                Darwin.close(fd)
                throw ready == 0 ? SocketError("Connecting timed out") : SocketError.errno()
            }
            var failure: Int32 = 0
            var length = socklen_t(MemoryLayout<Int32>.size)
            getsockopt(fd, SOL_SOCKET, SO_ERROR, &failure, &length)
            if failure != 0 {
                Darwin.close(fd)
                throw SocketError.errno(failure)
            }
        }
        return fd
    }

    // open connects, retrying refusals for wait ms: after a connection closes, the recorder
    // refuses new ones for a few seconds.
    static func open(wifi: HostWifi, host: String, port: Int, wait: Int64) throws -> Transfer {
        let deadline = Clock.now() + wait
        while true {
            let last: Error
            do {
                return try Transfer(socket: wifi.connect(host: host, port: port, timeout: 3_000))
            } catch {
                last = error
            }
            if Clock.now() >= deadline {
                throw PocketError("transfer", "The Pocket didn't accept a connection (\(last))")
            }
            try Interrupt.sleep(500)
        }
    }

    // alive says whether the connection is still open: not reset, closed or ended by the
    // recorder. The recorder sends nothing before the switch, so a short look only finds out
    // whether it's still there; a byte that does come stays for receive().
    func alive() -> Bool {
        lock.lock()
        defer { lock.unlock() }
        if lost != nil || closed { return false }
        var pfd = pollfd(fd: fd, events: Int16(POLLIN), revents: 0)
        if Darwin.poll(&pfd, 1, 1) <= 0 { return true }
        var byte: UInt8 = 0
        let n = recv(fd, &byte, 1, MSG_PEEK)
        if n > 0 { return true }
        if n == 0 {
            lost = "closed"
            return false
        }
        let code = Darwin.errno
        if code == EAGAIN || code == EWOULDBLOCK || code == EINTR { return true }
        lost = String(cString: strerror(code))
        return false
    }

    var lostReason: String {
        lock.lock()
        defer { lock.unlock() }
        return lost ?? "closed"
    }

    // abort drops the connection at once (cancel): a receive() under way fails.
    func abort() {
        lock.lock()
        defer { lock.unlock() }
        if !closed { Darwin.shutdown(fd, SHUT_RDWR) }
    }

    // close ends the connection gracefully (FIN, as verified on the recorder) and returns once
    // the recorder closed its side too, closing it after ms at the latest.
    func close(_ ms: Int32) {
        lock.lock()
        if closed {
            lock.unlock()
            return
        }
        lock.unlock()
        Darwin.shutdown(fd, SHUT_WR)
        let end = Clock.now() + Int64(ms)
        var skip = [UInt8](repeating: 0, count: 4096)
        while true {
            let left = end - Clock.now()
            if left <= 0 { break }
            var pfd = pollfd(fd: fd, events: Int16(POLLIN), revents: 0)
            if Darwin.poll(&pfd, 1, Int32(left)) <= 0 { break }
            let n = recv(fd, &skip, skip.count, 0)
            if n == 0 { break }
            if n < 0 && Darwin.errno != EAGAIN && Darwin.errno != EINTR { break }
            // whatever still comes is thrown away
        }
        lock.lock()
        if !closed {
            closed = true
            Darwin.close(fd)
        }
        lock.unlock()
    }

    // receive reads exactly size bytes of file from the connection into file, then the end
    // marker. It writes to <file>.part and renames it only once all of it arrived. Returns
    // whether the end marker followed (a missing one doesn't fail an otherwise complete file).
    func receive(size: Int64, to file: URL, timeouts t: Timeouts, progress: ((Int64, Int64) -> Void)?) throws -> Bool {
        let fm = FileManager.default
        let partial = URL(fileURLWithPath: file.path + ".part")
        try fm.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        guard fm.createFile(atPath: partial.path, contents: nil), let out = try? FileHandle(forWritingTo: partial) else {
            throw PocketError("transfer", "Couldn't write " + partial.lastPathComponent)
        }
        var ok = false
        defer { if !ok { try? fm.removeItem(at: partial) } }
        var received: Int64 = 0
        var tail: [UInt8] = []
        var buffer = [UInt8](repeating: 0, count: 64 * 1024)
        var timeout = size == 0 ? t.marker : t.firstByte
        do {
            defer { try? out.close() }
            while true {
                var pfd = pollfd(fd: fd, events: Int16(POLLIN), revents: 0)
                let ready = Darwin.poll(&pfd, 1, timeout)
                if ready == 0 {
                    if received >= size { break }
                    throw PocketError("transfer", received == 0 ? "The Pocket sent nothing" : "The transfer stalled at \(received) of \(size) bytes")
                }
                var n = 0
                if ready > 0 { n = recv(fd, &buffer, buffer.count, 0) }
                if ready < 0 || n < 0 {
                    let code = Darwin.errno
                    if code == EAGAIN || code == EWOULDBLOCK || code == EINTR { continue }
                    if received >= size { break }
                    throw PocketError("transfer", String(cString: strerror(code)))
                }
                if n == 0 {
                    if received >= size { break }
                    throw PocketError("transfer", "The Pocket closed the connection at \(received) of \(size) bytes")
                }
                let body = Int(min(Int64(n), size - received))
                if body > 0 {
                    try out.write(contentsOf: Data(buffer[0..<body]))
                    received += Int64(body)
                    progress?(received, size)
                }
                if n > body { tail.append(contentsOf: buffer[body..<n]) }
                if received >= size {
                    if tail.count >= Self.endMarker.count { break }
                    timeout = t.marker
                } else {
                    timeout = t.idle
                }
            }
        }
        try? fm.removeItem(at: file)
        do {
            try fm.moveItem(at: partial, to: file)
        } catch {
            throw PocketError("transfer", "Couldn't rename " + partial.lastPathComponent)
        }
        ok = true
        return tail.count >= Self.endMarker.count && Array(tail.prefix(Self.endMarker.count)) == Self.endMarker
    }
}
