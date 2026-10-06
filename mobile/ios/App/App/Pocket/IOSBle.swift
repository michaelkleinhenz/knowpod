import CoreBluetooth
import Foundation

// IOSBle is the Bluetooth LE connection to a Pocket recorder on the iPhone: it finds the
// recorder, connects, and carries the commands of a PocketSession (the counterpart of
// mobile/android/…/pocket/AndroidBle.java). CoreBluetooth runs on a queue of its own; the
// calls here wait for its callbacks, on the thread of the copy.
//
// iOS doesn't tell Bluetooth addresses (like macOS, see desktop/src/pocket-bluetooth.js), so
// the recorder is found by what it advertises: the device iOS knows from an earlier
// connection to the recorder at this address (PocketSettings.peripheral), one whose
// manufacturer data carries the address, or else the only device nearby whose name is a
// Pocket's ("PKT01_…"). The session key proves it is the right one.
final class IOSBle: NSObject, PocketLink, CBCentralManagerDelegate, CBPeripheralDelegate {
    static let service = CBUUID(string: "001120a0-2233-4455-6677-889912345678")
    static let commandUUID = CBUUID(string: "001120a3-2233-4455-6677-889912345678")
    // The recording data of a Bluetooth transfer (see PocketLink.subscribeAudio).
    static let audioUUID = CBUUID(string: "001120a1-2233-4455-6677-889912345678")

    // How long to look for the recorder before giving up (it may be asleep), ms.
    private static let scanTimeout: Int64 = 20_000
    // How long to look for a recorder known from before, before taking another Pocket.
    private static let knownWait: Int64 = 5_000
    // How long to keep looking once a Pocket was seen by name, for others nearby.
    private static let nameWait: Int64 = 2_000
    private static let connectTimeout: Int64 = 20_000
    private static let opTimeout: Int64 = 5_000

    private let queue = DispatchQueue(label: "net.kleinhenz.knowpod.pocket-ble")
    private let log = MessageLog()
    private let condition = NSCondition()
    private var central: CBCentralManager!
    private var peripheral: CBPeripheral?
    private var command: CBCharacteristic?
    private var audio: CBCharacteristic?
    private var audioListener: ((Data) -> Void)?

    // Guarded by condition: the operation waited for and its outcome (nil: ok, else why it
    // failed), and what the scan found.
    private var waiting: String?
    private var outcome: String??
    private var connected = false
    private var managerState: CBManagerState = .unknown
    // A write without response can go out: iOS said so (peripheralIsReady).
    private var canWrite = false
    // Writes go out one at a time, in order (the heartbeat writes from a timer).
    private let writeLock = NSLock()
    private var discovered: [UUID: (peripheral: CBPeripheral, name: String, data: Data?)] = [:]

    private let address: String
    private let known: UUID?

    private init(address: String, known: UUID?) {
        self.address = address
        self.known = known
        super.init()
    }

    // connect finds the recorder at address (known: the device found for it before), connects
    // and subscribes to its answers. The session still has to be unlocked (PocketSession.unlock).
    // Returns the session and the device iOS knows the recorder as.
    static func connect(address: String, known: UUID?) throws -> (session: PocketSession, peripheral: UUID) {
        let ble = IOSBle(address: address, known: known)
        do {
            let id = try ble.open()
            return (PocketSession(link: ble, log: ble.log), id)
        } catch {
            ble.close()
            throw error
        }
    }

    private func open() throws -> UUID {
        condition.lock()
        waiting = "state"
        outcome = nil
        condition.unlock()
        queue.sync {
            central = CBCentralManager(delegate: self, queue: queue, options: [CBCentralManagerOptionShowPowerAlertKey: false])
        }
        // The first time, iOS asks whether knowpod may use Bluetooth: wait for the answer.
        let stateWait: Int64 = CBManager.authorization == .notDetermined ? 120_000 : Self.opTimeout
        _ = try waitDone("state", stateWait)
        try Self.checkState(queue.sync { central.state })

        let device = try scan()
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
            throw PocketError("failed", "This device isn't a Pocket")
        }
        begin("characteristics")
        queue.sync { device.discoverCharacteristics([Self.commandUUID, Self.audioUUID], for: service) }
        if try waitDone("characteristics", 15_000) != .some(nil) { throw PocketError("failed", "Couldn't read the services") }
        command = service.characteristics?.first(where: { $0.uuid == Self.commandUUID })
        audio = service.characteristics?.first(where: { $0.uuid == Self.audioUUID })
        guard let command else { throw PocketError("failed", "This device isn't a Pocket") }
        try subscribe(command)
        return device.identifier
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

    private var isConnected: Bool {
        condition.lock()
        defer { condition.unlock() }
        return connected
    }

    // addressBytes are the six bytes of the address, as manufacturer data may carry them.
    private var addressBytes: Data {
        let hex = address.replacingOccurrences(of: ":", with: "")
        var bytes = Data()
        var index = hex.startIndex
        while index < hex.endIndex, let next = hex.index(index, offsetBy: 2, limitedBy: hex.endIndex) {
            if let byte = UInt8(hex[index..<next], radix: 16) { bytes.append(byte) }
            index = next
        }
        return bytes
    }

    // scan finds the recorder: it answers only while it advertises (awake, and not connected to
    // another phone, e.g. the Pocket app).
    private func scan() throws -> CBPeripheral {
        let wanted = addressBytes
        let reversed = Data(wanted.reversed())
        let carriesAddress: (Data?) -> Bool = { data in
            guard let data, wanted.count == 6 else { return false }
            return data.range(of: wanted) != nil || data.range(of: reversed) != nil
        }
        queue.sync {
            central.scanForPeripherals(withServices: nil, options: [CBCentralManagerScanOptionAllowDuplicatesKey: false])
        }
        defer { queue.sync { if central.state == .poweredOn { central.stopScan() } } }
        let started = Clock.now()
        var pocketSeen: Int64?
        condition.lock()
        defer { condition.unlock() }
        let interrupt = Interrupt.current
        while true {
            if interrupt.isRaised { throw PocketError("cancelled") }
            if managerState != .poweredOn { try Self.checkState(managerState) }
            let now = Clock.now()
            if let known, let found = discovered[known] { return found.peripheral }
            if let found = discovered.values.first(where: { carriesAddress($0.data) }) { return found.peripheral }
            let pockets = discovered.values.filter { $0.name.uppercased().hasPrefix("PKT01") }
            if !pockets.isEmpty && pocketSeen == nil { pocketSeen = now }
            // A recorder known from before gets a moment to show up before another one is taken.
            let knownGone = known == nil || now - started >= Self.knownWait
            if pockets.count == 1, let seen = pocketSeen, now - seen >= Self.nameWait, knownGone {
                return pockets[0].peripheral
            }
            if now - started >= Self.scanTimeout {
                if pockets.count > 1 { throw PocketError("not-found", "Several Pockets are nearby; keep only yours close") }
                throw PocketError("not-found", "The Pocket wasn't found")
            }
            condition.wait(until: Date(timeIntervalSinceNow: 0.1))
        }
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

    // complete ends operation (error: why it failed).
    private func complete(_ operation: String, _ error: Error?) {
        condition.lock()
        if waiting == operation {
            outcome = .some(error.map { $0.localizedDescription })
            condition.broadcast()
        }
        condition.unlock()
    }

    // subscribe turns on notifications of characteristic.
    private func subscribe(_ characteristic: CBCharacteristic) throws {
        guard let peripheral else { throw PocketError("disconnected", "The recorder disconnected") }
        begin("notify")
        queue.sync { peripheral.setNotifyValue(true, for: characteristic) }
        if try waitDone("notify", Self.opTimeout) != .some(nil) { throw PocketError("failed", "Couldn't subscribe") }
    }

    // write sends bytes to the command characteristic: without a response when it takes that,
    // as the Android app does, waiting while iOS's queue for those is full.
    func write(_ bytes: Data) throws {
        writeLock.lock()
        defer { writeLock.unlock() }
        condition.lock()
        let target = connected ? peripheral : nil
        let characteristic = command
        condition.unlock()
        guard let target, let characteristic else { throw PocketError("disconnected", "The recorder disconnected") }
        if characteristic.properties.contains(.writeWithoutResponse) {
            let end = Clock.now() + Self.opTimeout
            // Asked on Bluetooth's queue, never while holding the condition its callbacks take.
            while !queue.sync(execute: { target.canSendWriteWithoutResponse }) {
                condition.lock()
                if !canWrite { condition.wait(until: Date(timeIntervalSinceNow: 0.05)) }
                canWrite = false
                let gone = !connected
                condition.unlock()
                if gone { throw PocketError("disconnected", "The recorder disconnected") }
                try Interrupt.check()
                if Clock.now() >= end { throw PocketError("failed", "Writing took too long") }
            }
            queue.async { target.writeValue(bytes, for: characteristic, type: .withoutResponse) }
            return
        }
        begin("write")
        queue.async { target.writeValue(bytes, for: characteristic, type: .withResponse) }
        let result = try waitDone("write", Self.opTimeout)
        guard let result else { throw PocketError("failed", "Writing took too long") }
        if let reason = result { throw PocketError(isConnected ? "failed" : "disconnected", "Writing failed (\(reason))") }
    }

    func subscribeAudio(_ listener: @escaping (Data) -> Void) throws {
        guard let audio else { throw PocketError("failed", "The recorder has no audio characteristic") }
        condition.lock()
        audioListener = listener
        condition.unlock()
        try subscribe(audio)
    }

    func close() {
        condition.lock()
        connected = false
        condition.broadcast()
        condition.unlock()
        log.disconnect()
        queue.sync {
            guard let central else { return }
            if central.state == .poweredOn { central.stopScan() }
            if let peripheral { central.cancelPeripheralConnection(peripheral) }
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
            log.disconnect()
            if waiting != nil { outcome = .some("Bluetooth went off") }
        }
        condition.broadcast()
        condition.unlock()
    }

    func centralManager(_ central: CBCentralManager, didDiscover peripheral: CBPeripheral,
                        advertisementData: [String: Any], rssi RSSI: NSNumber) {
        let name = advertisementData[CBAdvertisementDataLocalNameKey] as? String ?? peripheral.name ?? ""
        let data = advertisementData[CBAdvertisementDataManufacturerDataKey] as? Data
        condition.lock()
        discovered[peripheral.identifier] = (peripheral, name, data)
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
        let was = connected
        connected = false
        // Whatever was waited for won't come.
        if waiting != nil && outcome == nil { outcome = .some(error?.localizedDescription ?? "The recorder disconnected") }
        condition.broadcast()
        condition.unlock()
        if was { log.disconnect() }
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

    func peripheralIsReady(toSendWriteWithoutResponse peripheral: CBPeripheral) {
        condition.lock()
        canWrite = true
        condition.broadcast()
        condition.unlock()
    }

    func peripheral(_ peripheral: CBPeripheral, didUpdateValueFor characteristic: CBCharacteristic, error: Error?) {
        guard error == nil, let value = characteristic.value else { return }
        if characteristic.uuid == Self.commandUUID {
            log.add(String(decoding: value, as: UTF8.self))
        } else if characteristic.uuid == Self.audioUUID {
            condition.lock()
            let listener = audioListener
            condition.unlock()
            listener?(value)
        }
    }
}
