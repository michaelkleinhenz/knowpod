import CoreBluetooth
import Foundation

// RecorderBle is the Bluetooth LE connection to a knowpod recorder (the ESP32 gadget, esp32/) on
// the iPhone, the counterpart of mobile/android/…/recorder/RecorderBle.java: it finds recorders by
// their transfer service, connects, and carries the requests of docs/ble-transfer.md. The
// recorder's characteristics need a paired link, so iOS pairs on the first request and asks the
// user for the passkey the recorder shows. CoreBluetooth runs on a queue of its own; the calls
// here wait for its callbacks, on the thread of the copy.
//
// iOS hides Bluetooth addresses: a recorder is known by the identifier iOS gives it.
final class RecorderBle: NSObject, RecorderLink, CBCentralManagerDelegate, CBPeripheralDelegate {
    static let service = CBUUID(string: "6b6e7000-0b1e-4d0a-9c3e-6b6e6f77706f")
    static let controlUUID = CBUUID(string: "6b6e7001-0b1e-4d0a-9c3e-6b6e6f77706f")
    static let dataUUID = CBUUID(string: "6b6e7002-0b1e-4d0a-9c3e-6b6e6f77706f")

    private static let connectTimeout: Int64 = 20_000
    private static let opTimeout: Int64 = 10_000
    private static let readTimeout: Int64 = 60_000

    struct Found {
        let id: UUID
        let name: String
    }

    private let queue = DispatchQueue(label: "net.kleinhenz.knowpod.recorder-ble")
    private let condition = NSCondition()
    private var central: CBCentralManager!
    // Guarded by condition
    private var peripheral: CBPeripheral?
    private var control: CBCharacteristic?
    private var waiting: String?
    private var outcome: String??
    private var connected = false
    private var managerState: CBManagerState = .unknown
    private var discovered: [UUID: (peripheral: CBPeripheral, name: String)] = [:]
    private var fragments = Data()
    private var response: [String: Any]?
    private var collected: Data?
    private var next: Int64 = 0
    private var gap = false

    // open starts Bluetooth and waits until it is ready (the first time, iOS asks whether knowpod
    // may use it).
    static func open() throws -> RecorderBle {
        let ble = RecorderBle()
        do {
            try ble.start()
        } catch {
            ble.close()
            throw error
        }
        return ble
    }

    private func start() throws {
        begin("state")
        queue.sync {
            central = CBCentralManager(delegate: self, queue: queue, options: [CBCentralManagerOptionShowPowerAlertKey: false])
        }
        let stateWait: Int64 = CBManager.authorization == .notDetermined ? 120_000 : Self.opTimeout
        _ = try waitDone("state", stateWait)
        try Self.checkState(queue.sync { central.state })
    }

    private static func checkState(_ state: CBManagerState) throws {
        switch state {
        case .poweredOn:
            return
        case .unauthorized:
            throw PocketError("permission", "Bluetooth permission not granted")
        case .unsupported:
            throw PocketError("unsupported", "This phone has no Bluetooth")
        case .poweredOff:
            throw PocketError("unsupported", "Bluetooth is off")
        default:
            throw PocketError("unsupported", "Bluetooth isn't ready")
        }
    }

    // scan looks for recorders advertising the transfer service for up to timeout ms: the one
    // known as known (if given), else all found, waiting settle ms after the first for others.
    func scan(known: UUID?, timeout: Int64, settle: Int64) throws -> [Found] {
        queue.sync {
            central.scanForPeripherals(withServices: [Self.service], options: [CBCentralManagerScanOptionAllowDuplicatesKey: false])
        }
        defer { queue.sync { if central.state == .poweredOn { central.stopScan() } } }
        let started = Clock.now()
        var firstAt: Int64?
        condition.lock()
        defer { condition.unlock() }
        let interrupt = Interrupt.current
        while true {
            if interrupt.isRaised { throw PocketError("cancelled") }
            if managerState != .poweredOn { try Self.checkState(managerState) }
            let now = Clock.now()
            if let known, let found = discovered[known] { return [Found(id: known, name: found.name)] }
            if known == nil && !discovered.isEmpty && firstAt == nil { firstAt = now }
            if let firstAt, now - firstAt >= settle { break }
            if now - started >= timeout { break }
            condition.wait(until: Date(timeIntervalSinceNow: 0.1))
        }
        return known == nil ? discovered.map { Found(id: $0.key, name: $0.value.name) } : []
    }

    // connect connects to a recorder found by scan and subscribes to its characteristics.
    func connect(_ id: UUID) throws {
        condition.lock()
        let device = discovered[id]?.peripheral
        condition.unlock()
        guard let device else { throw PocketError("not-found", "The recorder wasn't found") }
        // A first connection sometimes fails at once; a second try usually works.
        var attempt = 1
        while true {
            begin("connect")
            queue.sync { central.connect(device, options: nil) }
            let result = try waitDone("connect", Self.connectTimeout)
            if result == .some(nil) && isConnected { break }
            queue.sync { central.cancelPeripheralConnection(device) }
            if attempt >= 2 {
                if case .some(.some(let reason)) = result { throw PocketError("failed", "Connecting failed (\(reason))") }
                throw PocketError("timeout", "Connecting took too long")
            }
            attempt += 1
            try Interrupt.sleep(500)
        }

        begin("services")
        queue.sync { device.discoverServices([Self.service]) }
        if try waitDone("services", 15_000) != .some(nil) { throw PocketError("failed", "Couldn't read the services") }
        guard let service = device.services?.first(where: { $0.uuid == Self.service }) else {
            throw PocketError("failed", "This device isn't a knowpod recorder")
        }
        begin("characteristics")
        queue.sync { device.discoverCharacteristics([Self.controlUUID, Self.dataUUID], for: service) }
        if try waitDone("characteristics", 15_000) != .some(nil) { throw PocketError("failed", "Couldn't read the services") }
        let control = service.characteristics?.first(where: { $0.uuid == Self.controlUUID })
        let data = service.characteristics?.first(where: { $0.uuid == Self.dataUUID })
        guard let control, let data else { throw PocketError("failed", "This device isn't a knowpod recorder") }
        condition.lock()
        self.control = control
        condition.unlock()
        try subscribe(control)
        try subscribe(data)
    }

    private var isConnected: Bool {
        condition.lock()
        defer { condition.unlock() }
        return connected
    }

    private func begin(_ operation: String) {
        condition.lock()
        waiting = operation
        outcome = nil
        condition.unlock()
    }

    // waitDone waits for operation to end and returns its outcome (.some(nil): it worked,
    // .some(reason): it failed), or nil after timeout ms.
    private func waitDone(_ operation: String, _ timeout: Int64) throws -> String?? {
        condition.lock()
        defer {
            waiting = nil
            condition.unlock()
        }
        _ = try Waiter.wait(condition, timeout: timeout) { outcome != nil || waiting != operation }
        return outcome
    }

    private func complete(_ operation: String, _ error: Error?) {
        condition.lock()
        if waiting == operation {
            outcome = .some(error.map { $0.localizedDescription })
            condition.broadcast()
        }
        condition.unlock()
    }

    private func subscribe(_ characteristic: CBCharacteristic) throws {
        condition.lock()
        let target = peripheral
        condition.unlock()
        guard let target else { throw PocketError("disconnected", "The recorder disconnected") }
        begin("notify")
        queue.sync { target.setNotifyValue(true, for: characteristic) }
        if try waitDone("notify", Self.opTimeout) != .some(nil) { throw PocketError("failed", "Couldn't subscribe") }
    }

    // write sends a request; timeout covers the pairing iOS may run first.
    private func write(_ bytes: Data, timeout: Int64) throws {
        condition.lock()
        let target = connected ? peripheral : nil
        let characteristic = control
        condition.unlock()
        guard let target, let characteristic else { throw PocketError("disconnected", "The recorder disconnected") }
        begin("write")
        queue.async { target.writeValue(bytes, for: characteristic, type: .withResponse) }
        let result = try waitDone("write", max(timeout, Self.opTimeout))
        guard let result else { throw PocketError("timeout", "Writing took too long") }
        if let reason = result {
            if !isConnected { throw PocketError("disconnected", "The recorder disconnected") }
            throw PocketError(reason.lowercased().contains("auth") || reason.lowercased().contains("encrypt") ? "auth" : "failed",
                              "Writing failed (\(reason))")
        }
    }

    func request(_ message: [String: Any], timeout: Int64) throws -> [String: Any] {
        condition.lock()
        fragments = Data()
        response = nil
        condition.unlock()
        let started = Clock.now()
        try write(try JSONSerialization.data(withJSONObject: message), timeout: timeout)
        condition.lock()
        defer {
            response = nil
            condition.unlock()
        }
        let left = max(timeout - (Clock.now() - started), 1)
        _ = try Waiter.wait(condition, timeout: left) { response != nil || !connected }
        if let response { return response }
        if !connected { throw PocketError("disconnected", "The recorder disconnected") }
        throw PocketError("timeout", "No answer to \(message["op"] as? String ?? "")")
    }

    func read(id: String, offset: Int64, length: Int) throws -> Data {
        condition.lock()
        collected = Data(capacity: length)
        next = offset
        gap = false
        condition.unlock()
        defer {
            condition.lock()
            collected = nil
            condition.unlock()
        }
        let r = try request(["op": "read", "id": id, "offset": offset, "length": length], timeout: Self.readTimeout)
        guard r["ok"] as? Bool == true else {
            throw PocketError("recorder", "read: \(r["message"] as? String ?? r["error"] as? String ?? "failed")")
        }
        condition.lock()
        defer { condition.unlock() }
        return collected ?? Data()
    }

    func close() {
        condition.lock()
        connected = false
        let target = peripheral
        condition.broadcast()
        condition.unlock()
        queue.sync {
            guard let central else { return }
            if central.state == .poweredOn { central.stopScan() }
            if let target { central.cancelPeripheralConnection(target) }
            central.delegate = nil
        }
    }

    // MARK: CBCentralManagerDelegate, on queue

    func centralManagerDidUpdateState(_ central: CBCentralManager) {
        condition.lock()
        managerState = central.state
        if central.state != .unknown && central.state != .resetting && waiting == "state" {
            outcome = .some(nil)
        }
        if central.state != .poweredOn && connected {
            connected = false
            if waiting != nil { outcome = .some("Bluetooth went off") }
        }
        condition.broadcast()
        condition.unlock()
    }

    func centralManager(_ central: CBCentralManager, didDiscover peripheral: CBPeripheral,
                        advertisementData: [String: Any], rssi RSSI: NSNumber) {
        let name = advertisementData[CBAdvertisementDataLocalNameKey] as? String ?? peripheral.name ?? ""
        condition.lock()
        discovered[peripheral.identifier] = (peripheral, name)
        condition.broadcast()
        condition.unlock()
    }

    func centralManager(_ central: CBCentralManager, didConnect peripheral: CBPeripheral) {
        peripheral.delegate = self
        condition.lock()
        self.peripheral = peripheral
        connected = true
        condition.unlock()
        complete("connect", nil)
    }

    func centralManager(_ central: CBCentralManager, didFailToConnect peripheral: CBPeripheral, error: Error?) {
        complete("connect", error ?? PocketError("failed", "Connecting failed"))
    }

    func centralManager(_ central: CBCentralManager, didDisconnectPeripheral peripheral: CBPeripheral, error: Error?) {
        condition.lock()
        connected = false
        // Whatever was waited for won't come.
        if waiting != nil && outcome == nil { outcome = .some(error?.localizedDescription ?? "The recorder disconnected") }
        condition.broadcast()
        condition.unlock()
    }

    // MARK: CBPeripheralDelegate, on queue

    func peripheral(_ peripheral: CBPeripheral, didDiscoverServices error: Error?) {
        complete("services", error)
    }

    func peripheral(_ peripheral: CBPeripheral, didDiscoverCharacteristicsFor service: CBService, error: Error?) {
        complete("characteristics", error)
    }

    func peripheral(_ peripheral: CBPeripheral, didUpdateNotificationStateFor characteristic: CBCharacteristic, error: Error?) {
        complete("notify", error)
    }

    func peripheral(_ peripheral: CBPeripheral, didWriteValueFor characteristic: CBCharacteristic, error: Error?) {
        complete("write", error)
    }

    func peripheral(_ peripheral: CBPeripheral, didUpdateValueFor characteristic: CBCharacteristic, error: Error?) {
        guard error == nil, let value = characteristic.value, !value.isEmpty else { return }
        let bytes = [UInt8](value)
        condition.lock()
        defer { condition.unlock() }
        if characteristic.uuid == Self.controlUUID {
            fragments.append(contentsOf: bytes[1...])
            if bytes[0] & 1 != 0 { return }
            response = (try? JSONSerialization.jsonObject(with: fragments)) as? [String: Any]
            fragments = Data()
            condition.broadcast()
        } else if characteristic.uuid == Self.dataUUID, collected != nil, bytes.count >= 4 {
            let at = Int64(bytes[0]) | Int64(bytes[1]) << 8 | Int64(bytes[2]) << 16 | Int64(bytes[3]) << 24
            // A gap means a notification got lost: keep what came before it; the relay asks again.
            if at != next { gap = true }
            if gap { return }
            collected?.append(contentsOf: bytes[4...])
            next += Int64(bytes.count - 4)
        }
    }
}
