#include "ble.h"
#include <ArduinoJson.h>
#include <NimBLEDevice.h>
#include <atomic>
#include <map>
#include <esp_random.h>
#include "proc/upload.h"
#include "net/wifi.h"
#include "proc/worker.h"
#include "store/config.h"
#include "store/recordings.h"

// GATT service of the Bluetooth transfer (docs/ble-transfer.md):
//   CONTROL  write: one JSON request at a time (encrypted, authenticated link only)
//            notify: its JSON response, in fragments [flags][bytes], flags bit 0
//            set while more fragments follow
//   DATA     notify: the file bytes a "read" asked for, as [offset: uint32 LE][bytes]
//
// Requests: info, list, open (size and SHA-256 of one recording), read (a range
// of its file), done (the app's upload to the backend is complete). Responses
// carry "ok" and the request's "op"; failures "error" and "message".

#define SERVICE_UUID        "6b6e7000-0b1e-4d0a-9c3e-6b6e6f77706f"
#define CONTROL_UUID        "6b6e7001-0b1e-4d0a-9c3e-6b6e6f77706f"
#define DATA_UUID           "6b6e7002-0b1e-4d0a-9c3e-6b6e6f77706f"
#define PROTOCOL            1

#define BLE_STACK           8192
#define POLL_MS             1000
#define PAIRING_MS          180000
#define OFFER_AWAKE_MS      (20u * 60000)   // keeps the device awake while offering recordings
#define AFTER_PAIRING_MS    60000    // stays reachable for the app's first connection after pairing
#define MAX_REQUEST         512
#define MAX_READ            (256u * 1024)
#define MAX_LISTED          50
#define FILE_BUF_BYTES      8192
#define NOTIFY_TIMEOUT_MS   5000
#define IDLE_DISCONNECT_MS  300000   // an app that stays connected without asking anything
#define ADV_INTERVAL_MIN    160      // × 0.625 ms: 100 ms
#define ADV_INTERVAL_MAX    320      // 200 ms
#define ADV_SLOW_MIN        1600     // 1 s, while nothing waits
#define ADV_SLOW_MAX        2400     // 1.5 s

struct Request {
    uint16_t conn;
    uint16_t len;
    char     data[MAX_REQUEST + 1];
};

static TaskHandle_t task;
static QueueHandle_t requests;
static SemaphoreHandle_t mutex = xSemaphoreCreateMutex();

namespace {  // file-local: each file's Lock guards its own mutex
struct Lock {
    Lock()  { xSemaphoreTake(mutex, portMAX_DELAY); }
    ~Lock() { xSemaphoreGive(mutex); }
};
}

static volatile int offer = BLE_OFFER_NONE;
static bool adv_fast = false;                 // BLE task only: the interval advertising runs at
static volatile bool paused = false;
static volatile bool yielded = false;   // to Wi-Fi
static volatile bool running = false;
static volatile bool sending = false;
static volatile bool forget_requested = false;
static volatile uint16_t conn = BLE_HS_CONN_HANDLE_NONE;   // the connected app (one at a time)
static volatile uint16_t mtu = 23;
static volatile uint32_t pairing_until = 0;   // millis(); 0: not pairing
static volatile uint32_t paired_until = 0;    // millis(); 0: not just paired
static volatile uint32_t passkey = 0;         // shown while pairing
static volatile bool refuse_pairing = false;  // this connection tried to pair outside pairing mode
static volatile bool paired_now = false;
static volatile int paired_count = -1;        // -1: not known yet
static volatile uint32_t last_request = 0;
static volatile uint32_t offer_since = 0;     // millis() the recordings were last offered from
static std::atomic<uint32_t> generation{0};
static String activity, activity_short;       // guarded by mutex
static String name;

// Stack objects, BLE task only (the NimBLE callbacks only read the volatile state)
static NimBLEServer *server;
static NimBLECharacteristic *control, *data_chr;

// SHA-256 of recordings whose meta.json has none yet, computed for "open" (BLE task only)
struct Checksum {
    size_t size;
    String sha256;
};
static std::map<String, Checksum> checksums;

static void changed() { generation++; }

static bool pairing_active()
{
    uint32_t until = pairing_until;
    return until && (int32_t)(until - millis()) > 0;
}

// Just paired: Bluetooth stays on a while even with Wi-Fi, so an app that reconnects
// right after bonding (Android may) still finds the recorder. No new pairing meanwhile.
static bool just_paired()
{
    uint32_t until = paired_until;
    return until && (int32_t)(until - millis()) > 0;
}

static void set_activity(const String &full, const String &brief)
{
    Lock lock;
    if (full == activity && brief == activity_short) return;
    activity = full;
    activity_short = brief;
    changed();
    if (!full.isEmpty()) Serial.printf("[ble] %s\n", full.c_str());
}

// ============================================================
// NimBLE callbacks (NimBLE host task)
// ============================================================

class ServerCallbacks : public NimBLEServerCallbacks {
    void onConnect(NimBLEServer *s, NimBLEConnInfo &info) override
    {
        if (conn != BLE_HS_CONN_HANDLE_NONE) {
            s->disconnect(info);
            return;
        }
        conn = info.getConnHandle();
        mtu = 23;
        refuse_pairing = false;
        last_request = millis();
        // A short connection interval and long packets: recordings are large
        s->updateConnParams(conn, 6, 24, 0, 500);
        s->setDataLen(conn, 251);
        // Ask the app to encrypt right away: a paired app does so silently, a new one pairs
        NimBLEDevice::startSecurity(conn);
        Serial.printf("[ble] %s connected\n", info.getAddress().toString().c_str());
        changed();
    }

    void onDisconnect(NimBLEServer *, NimBLEConnInfo &info, int reason) override
    {
        if (info.getConnHandle() != conn) return;
        conn = BLE_HS_CONN_HANDLE_NONE;
        offer_since = millis();
        Serial.printf("[ble] app disconnected (reason 0x%x)\n", reason);
        changed();
    }

    void onMTUChange(uint16_t value, NimBLEConnInfo &info) override
    {
        if (info.getConnHandle() == conn) mtu = value;
    }

    uint32_t onPassKeyDisplay() override
    {
        uint32_t key = 100000 + esp_random() % 900000;
        if (pairing_active()) {
            passkey = key;
            Serial.println("[ble] pairing: showing the passkey");
        } else {
            refuse_pairing = true;
            Serial.println("[ble] pairing refused: pairing is not switched on");
        }
        changed();
        return key;
    }

    void onAuthenticationComplete(NimBLEConnInfo &info) override
    {
        if (info.getConnHandle() != conn) return;
        bool encrypted = info.isEncrypted();
        Serial.printf("[ble] security: encrypted %d, authenticated %d, bonded %d\n",
                      encrypted, info.isAuthenticated(), info.isBonded());
        if (!encrypted || !info.isAuthenticated() || refuse_pairing) {
            // A bond made outside pairing mode, or without a passkey (Just Works), is not kept
            if (refuse_pairing || (encrypted && !info.isAuthenticated()))
                NimBLEDevice::deleteBond(info.getIdAddress());
            Serial.println("[ble] link not authenticated; disconnecting");
            NimBLEDevice::getServer()->disconnect(info);
            return;
        }
        if (passkey) {
            Serial.println("[ble] paired with a new app");
            passkey = 0;
            pairing_until = 0;
            paired_until = millis() + AFTER_PAIRING_MS;
            if (!paired_until) paired_until = 1;
            paired_now = true;
            paired_count = NimBLEDevice::getNumBonds();
        }
        changed();
    }
};

class ControlCallbacks : public NimBLECharacteristicCallbacks {
    void onWrite(NimBLECharacteristic *c, NimBLEConnInfo &info) override
    {
        // The stack already refuses writes without an authenticated link; this is a second check
        if (info.getConnHandle() != conn || !info.isEncrypted() || !info.isAuthenticated()) {
            Serial.printf("[ble] request ignored (current %d, encrypted %d, authenticated %d)\n",
                          info.getConnHandle() == conn, info.isEncrypted(), info.isAuthenticated());
            return;
        }
        static Request req;  // host task only; too large for its stack
        NimBLEAttValue value = c->getValue();
        req.conn = info.getConnHandle();
        req.len = min<size_t>(value.size(), MAX_REQUEST);
        memcpy(req.data, value.data(), req.len);
        req.data[req.len] = 0;
        if (xQueueSend(requests, &req, 0) != pdTRUE) Serial.println("[ble] request dropped: still busy");
    }

    void onSubscribe(NimBLECharacteristic *c, NimBLEConnInfo &, uint16_t value) override
    {
        Serial.printf("[ble] app %s %s\n", value ? "subscribed to" : "unsubscribed from",
                      c == control ? "control" : "data");
    }
};

// ============================================================
// Sending (BLE task)
// ============================================================

// Notifies `to`, waiting while the stack has no buffers free. False once the
// app is gone or the stack stays stuck.
static bool notify(NimBLECharacteristic *c, const uint8_t *buf, size_t n, uint16_t to)
{
    uint32_t start = millis();
    while (conn == to && !paused) {
        if (c->notify(buf, n, to)) return true;
        if (millis() - start > NOTIFY_TIMEOUT_MS) return false;
        vTaskDelay(pdMS_TO_TICKS(3));
    }
    return false;
}

static void respond(uint16_t to, const JsonDocument &doc)
{
    String json;
    serializeJson(doc, json);
    size_t fragment = max<int>(mtu - 3 - 1, 19);
    static uint8_t buf[520];
    fragment = min(fragment, sizeof(buf) - 1);
    size_t pos = 0;
    do {
        size_t n = min(fragment, json.length() - pos);
        buf[0] = pos + n < json.length() ? 1 : 0;
        memcpy(buf + 1, json.c_str() + pos, n);
        if (!notify(control, buf, n + 1, to)) {
            Serial.printf("[ble] response to %s not sent\n", doc["op"].as<const char *>());
            return;
        }
        pos += n;
    } while (pos < json.length());
}

static void respond_error(uint16_t to, const String &op, const char *code, const String &message)
{
    JsonDocument doc;
    doc["ok"] = false;
    doc["op"] = op;
    doc["error"] = code;
    doc["message"] = message;
    respond(to, doc);
}

// ============================================================
// Requests (BLE task)
// ============================================================

static bool offered(const RecordingInfo &r)
{
    // Highlights of uploaded recordings go over Wi-Fi only; the app relays whole files
    return worker_needs_upload(r) && r.upload != "done";
}

static String recorded_at(const JsonDocument &meta)
{
    uint32_t created = meta["created_unix"] | 0;
    if (!created) return String();
    time_t t = created;
    tm utc;
    gmtime_r(&t, &utc);
    char iso[32];
    strftime(iso, sizeof(iso), "%Y-%m-%dT%H:%M:%SZ", &utc);
    return iso;
}

static void handle_info(uint16_t to)
{
    int pending = 0;
    for (const RecordingInfo &r : recordings_list())
        if (offered(r)) pending++;
    JsonDocument doc;
    doc["ok"] = true;
    doc["op"] = "info";
    doc["protocol"] = PROTOCOL;
    doc["name"] = name;
    // The app uploads with the device's own token, so the backend files the
    // recordings under this device and a later Wi-Fi upload finds them done
    bool backend = config_backend_enabled();
    doc["backend"] = backend ? config_backend_url() : "";
    doc["token"] = backend ? config_backend_token() : "";
    doc["pending"] = pending;
    respond(to, doc);
}

static void handle_list(uint16_t to)
{
    std::vector<RecordingInfo> list = recordings_list();
    JsonDocument doc;
    doc["ok"] = true;
    doc["op"] = "list";
    JsonArray out = doc["recordings"].to<JsonArray>();
    int listed = 0;
    for (auto it = list.rbegin(); it != list.rend() && listed < MAX_LISTED; ++it) {  // oldest first
        if (!offered(*it)) continue;
        JsonDocument meta;
        if (!recording_load_meta(it->id, meta)) continue;
        File f = recordings_fs().open(recording_audio_path(it->id), FILE_READ);
        if (!f) continue;
        size_t size = f.size();
        f.close();

        JsonObject rec = out.add<JsonObject>();
        rec["id"] = it->id;
        rec["size"] = size;
        rec["duration"] = it->duration_s;
        rec["title"] = recording_display_title(*it);
        String at = recorded_at(meta);
        if (!at.isEmpty()) rec["recordedAt"] = at;
        JsonArray hl = rec["highlights"].to<JsonArray>();
        for (float h : meta["highlights"].as<JsonArrayConst>()) hl.add((int64_t)lroundf(h * 1000));
        listed++;
    }
    respond(to, doc);
}

// Size and SHA-256 of a recording's file: from meta.json when the Wi-Fi upload
// already hashed it, else computed once per boot.
static bool checksum(const String &id, const JsonDocument &meta, size_t &size, String &sha256)
{
    File f = recordings_fs().open(recording_audio_path(id), FILE_READ);
    if (!f) return false;
    size = f.size();
    String known = meta["upload"]["sha256"] | "";
    if (!known.isEmpty() && (size_t)(meta["upload"]["size"] | 0) == size) {
        f.close();
        sha256 = known;
        return true;
    }
    auto it = checksums.find(id);
    if (it != checksums.end() && it->second.size == size) {
        f.close();
        sha256 = it->second.sha256;
        return true;
    }
    bool ok = sha256_file(f, sha256);
    f.close();
    if (ok) checksums[id] = {size, sha256};
    return ok;
}

static bool load(uint16_t to, const String &op, const String &id, RecordingInfo &info, JsonDocument &meta)
{
    if (!recording_valid_id(id) || !recording_info(id, info) || !recording_load_meta(id, meta)) {
        respond_error(to, op, "not-found", "No recording " + id);
        return false;
    }
    return true;
}

static void handle_open(uint16_t to, const String &id)
{
    RecordingInfo info;
    JsonDocument meta;
    if (!load(to, "open", id, info, meta)) return;
    set_activity("Preparing " + recording_display_title(info), "BT");
    size_t size;
    String sha256;
    if (!checksum(id, meta, size, sha256)) {
        respond_error(to, "open", "io", "Cannot read the audio file");
        return;
    }
    JsonDocument doc;
    doc["ok"] = true;
    doc["op"] = "open";
    doc["id"] = id;
    doc["size"] = size;
    doc["sha256"] = sha256;
    respond(to, doc);
}

static void handle_read(uint16_t to, const String &id, uint32_t offset, uint32_t length)
{
    RecordingInfo info;
    JsonDocument meta;
    if (!load(to, "read", id, info, meta)) return;
    File f = recordings_fs().open(recording_audio_path(id), FILE_READ);
    if (!f) {
        respond_error(to, "read", "io", "Cannot read the audio file");
        return;
    }
    size_t size = f.size();
    if (offset > size) {
        f.close();
        respond_error(to, "read", "bad-request", "Offset beyond the end of the file");
        return;
    }
    uint32_t n = min<uint32_t>(min<uint32_t>(length, MAX_READ), size - offset);
    int percent = size ? (int)(100.0 * (offset + n) / size) : 100;
    set_activity("Sending " + recording_display_title(info) + " (" + String(percent) + "%)",
                 "BT " + String(percent) + "%");

#ifdef BOARD_HAS_PSRAM
    static uint8_t *file_buf = (uint8_t *)ps_malloc(FILE_BUF_BYTES);
#else
    static uint8_t *file_buf = (uint8_t *)malloc(FILE_BUF_BYTES);
#endif
    static uint8_t packet[520];
    size_t payload = min<size_t>(max<int>(mtu - 3 - 4, 16), sizeof(packet) - 4);
    sending = true;
    uint32_t sent = 0;
    bool ok = file_buf && f.seek(offset);
    while (ok && sent < n) {
        size_t got = f.read(file_buf, min<size_t>(FILE_BUF_BYTES, n - sent));
        if (got == 0) {
            ok = false;
            break;
        }
        for (size_t pos = 0; ok && pos < got; pos += payload) {
            size_t len = min(payload, got - pos);
            uint32_t at = offset + sent + pos;
            packet[0] = at & 0xff;
            packet[1] = (at >> 8) & 0xff;
            packet[2] = (at >> 16) & 0xff;
            packet[3] = (at >> 24) & 0xff;
            memcpy(packet + 4, file_buf + pos, len);
            ok = notify(data_chr, packet, len + 4, to);
        }
        if (ok) sent += got;
    }
    f.close();
    sending = false;
    Serial.printf("[ble] read %s: %u of %u bytes from %u sent in packets of %u (MTU %u)%s\n", id.c_str(),
                  (unsigned)sent, (unsigned)n, (unsigned)offset, (unsigned)payload, (unsigned)mtu,
                  ok ? "" : ", stopped");
    if (conn != to) return;  // gone; the app asks again from where it got to
    if (!ok && sent == 0) {
        respond_error(to, "read", "io", "Cannot read the audio file");
        return;
    }
    JsonDocument doc;
    doc["ok"] = true;
    doc["op"] = "read";
    doc["id"] = id;
    doc["offset"] = offset;
    doc["length"] = sent;
    respond(to, doc);
}

static bool remote_complete(const String &status)
{
    return status == "received" || status == "stored" || status == "transcribed" || status == "summarized";
}

static void handle_done(uint16_t to, const String &id, const String &upload_id, const String &status)
{
    RecordingInfo info;
    JsonDocument meta;
    if (!load(to, "done", id, info, meta)) return;
    if (upload_id.isEmpty() || !remote_complete(status)) {
        respond_error(to, "done", "bad-request", "uploadId and a completed status are required");
        return;
    }
    size_t size;
    String sha256;
    if (!checksum(id, meta, size, sha256)) {
        respond_error(to, "done", "io", "Cannot read the audio file");
        return;
    }
    worker_ble_uploaded(id, upload_id, sha256, size, status);
    checksums.erase(id);
    set_activity("Sent " + recording_display_title(info) + " to the app", "BT");

    JsonDocument doc;
    doc["ok"] = true;
    doc["op"] = "done";
    doc["id"] = id;
    respond(to, doc);
}

static void handle(const Request &req)
{
    JsonDocument in;
    if (deserializeJson(in, req.data, req.len) || !in.is<JsonObject>()) {
        respond_error(req.conn, "", "bad-request", "Not a JSON object");
        return;
    }
    String op = in["op"] | "";
    String id = in["id"] | "";
    Serial.printf("[ble] request %s %s\n", op.c_str(), id.c_str());
    if (op == "info") handle_info(req.conn);
    else if (op == "list") handle_list(req.conn);
    else if (op == "open") handle_open(req.conn, id);
    else if (op == "read") handle_read(req.conn, id, in["offset"] | 0u, in["length"] | 0u);
    else if (op == "done") handle_done(req.conn, id, in["uploadId"] | "", in["status"] | "");
    else respond_error(req.conn, op, "bad-request", "Unknown op");
}

// ============================================================
// Stack (BLE task)
// ============================================================

// Quickly found while pairing or while recordings wait; slowly otherwise
static bool fast_advertising()
{
    return offer == BLE_OFFER_WAITING || pairing_active() || just_paired();
}

static void start()
{
    wifi_radio_lock();
    NimBLEDevice::init(name.c_str());
    wifi_radio_unlock();
    NimBLEDevice::setMTU(517);
    // Bonding, MITM protection (passkey) and LE Secure Connections
    NimBLEDevice::setSecurityAuth(true, true, true);
    NimBLEDevice::setSecurityIOCap(BLE_HS_IO_DISPLAY_ONLY);

    server = NimBLEDevice::createServer();
    server->setCallbacks(new ServerCallbacks());
    NimBLEService *service = server->createService(SERVICE_UUID);
    control = service->createCharacteristic(
        CONTROL_UUID,
        NIMBLE_PROPERTY::WRITE | NIMBLE_PROPERTY::WRITE_ENC | NIMBLE_PROPERTY::WRITE_AUTHEN | NIMBLE_PROPERTY::NOTIFY,
        MAX_REQUEST);
    static ControlCallbacks control_callbacks;
    control->setCallbacks(&control_callbacks);
    data_chr = service->createCharacteristic(DATA_UUID, NIMBLE_PROPERTY::NOTIFY);
    data_chr->setCallbacks(&control_callbacks);  // for the subscribe log
    server->start();

    NimBLEAdvertisementData adv_data, scan_data;
    adv_data.setFlags(BLE_HS_ADV_F_DISC_GEN | BLE_HS_ADV_F_BREDR_UNSUP);
    adv_data.addServiceUUID(NimBLEUUID(SERVICE_UUID));
    scan_data.setName(name.c_str());
    NimBLEAdvertising *adv = NimBLEDevice::getAdvertising();
    adv->setAdvertisementData(adv_data);
    adv->setScanResponseData(scan_data);
    adv->enableScanResponse(true);
    adv_fast = fast_advertising();
    adv->setMinInterval(adv_fast ? ADV_INTERVAL_MIN : ADV_SLOW_MIN);
    adv->setMaxInterval(adv_fast ? ADV_INTERVAL_MAX : ADV_SLOW_MAX);
    adv->start();

    paired_count = NimBLEDevice::getNumBonds();
    running = true;
    Serial.printf("[ble] advertising as %s (%d paired apps)\n", name.c_str(), (int)paired_count);
    changed();
}

static void stop()
{
    if (conn != BLE_HS_CONN_HANDLE_NONE && server) server->disconnect(conn);
    wifi_radio_lock();
    NimBLEDevice::deinit(true);  // frees the stack's memory; bonds stay in NVS
    wifi_radio_unlock();
    server = nullptr;
    control = data_chr = nullptr;
    conn = BLE_HS_CONN_HANDLE_NONE;
    running = false;
    set_activity("", "");
    Serial.println("[ble] off");
    changed();
}

static void ble_task(void *)
{
    for (;;) {
        // The number of paired apps, read once; the stack must run for that, so not beside Wi-Fi
        if (paired_count < 0 && !yielded && !running) {
            wifi_radio_lock();
            NimBLEDevice::init(name.c_str());
            paired_count = NimBLEDevice::getNumBonds();
            NimBLEDevice::deinit(true);
            wifi_radio_unlock();
            changed();
        }

        Request req;
        if (xQueueReceive(requests, &req, pdMS_TO_TICKS(POLL_MS)) == pdTRUE) {
            last_request = millis();
            if (req.conn == conn && !paused) handle(req);
            last_request = millis();  // a long request (hashing, reading) counts as activity
            continue;
        }

        if (paired_until && !just_paired()) paired_until = 0;
        if (pairing_until && !pairing_active()) {
            pairing_until = 0;
            passkey = 0;
            changed();
        }
        if (forget_requested && (running || !yielded)) {  // the stack must not start beside Wi-Fi
            forget_requested = false;
            bool was_running = running;
            if (!was_running) {
                wifi_radio_lock();
                NimBLEDevice::init(name.c_str());
                wifi_radio_unlock();
            }
            if (conn != BLE_HS_CONN_HANDLE_NONE && server) server->disconnect(conn);
            NimBLEDevice::deleteAllBonds();
            paired_count = NimBLEDevice::getNumBonds();
            if (!was_running) {
                wifi_radio_lock();
                NimBLEDevice::deinit(true);
                wifi_radio_unlock();
            }
            checksums.clear();
            Serial.println("[ble] forgot all paired apps");
            changed();
        }

        bool connected = conn != BLE_HS_CONN_HANDLE_NONE;
        if (connected && server && millis() - last_request > IDLE_DISCONNECT_MS) server->disconnect(conn);
        if (!connected) set_activity("", "");

        bool enabled = config_bluetooth_enabled();
        bool offered = offer == BLE_OFFER_WAITING || (offer == BLE_OFFER_IDLE && paired_count > 0);
        bool want = enabled && !paused && !yielded && (offered || pairing_active() || just_paired());
        if (want && !running) start();
        else if (running && (!enabled || paused || yielded || (!want && !connected))) stop();
        else if (running && !connected && fast_advertising() != adv_fast) {
            NimBLEAdvertising *adv = NimBLEDevice::getAdvertising();
            adv->stop();
            adv_fast = !adv_fast;
            adv->setMinInterval(adv_fast ? ADV_INTERVAL_MIN : ADV_SLOW_MIN);
            adv->setMaxInterval(adv_fast ? ADV_INTERVAL_MAX : ADV_SLOW_MAX);
            adv->start();
        }
    }
}

// ============================================================
// Public API
// ============================================================

void ble_begin()
{
    uint64_t mac = ESP.getEfuseMac();
    char buf[20];
    snprintf(buf, sizeof(buf), "knowpod-%02X%02X", (uint8_t)(mac >> 32), (uint8_t)(mac >> 40));
    name = buf;
    requests = xQueueCreate(2, sizeof(Request));
    xTaskCreatePinnedToCore(ble_task, "ble", BLE_STACK, nullptr, 1, &task, 0);
}

void ble_offer(BleOffer o)
{
    if (offer == o) return;
    if (o == BLE_OFFER_WAITING) offer_since = millis();
    offer = o;
    changed();
}

void ble_yield(bool y)
{
    if (yielded == y) return;
    yielded = y;
    changed();
}

void ble_pause(bool p)
{
    paused = p;
    changed();
}

bool ble_running()   { return running; }
bool ble_connected() { return conn != BLE_HS_CONN_HANDLE_NONE; }
bool ble_sending()   { return sending; }

bool ble_offering()
{
    return offer == BLE_OFFER_WAITING && running && !paused && millis() - offer_since < OFFER_AWAKE_MS;
}

void ble_start_pairing()
{
    paired_now = false;
    passkey = 0;
    pairing_until = millis() + PAIRING_MS;
    if (!pairing_until) pairing_until = 1;
    changed();
}

void ble_stop_pairing()
{
    pairing_until = 0;
    passkey = 0;
    changed();
}

bool ble_pairing()       { return pairing_active(); }
bool ble_paired_now()    { return paired_now; }
int ble_paired_count()   { return paired_count; }
uint32_t ble_generation() { return generation; }
String ble_name()        { return name; }

String ble_passkey()
{
    uint32_t key = passkey;
    if (!key) return String();
    char buf[8];
    snprintf(buf, sizeof(buf), "%06u", (unsigned)key);
    return buf;
}

void ble_forget_all()
{
    forget_requested = true;
    changed();
}

String ble_status()
{
    if (!config_bluetooth_enabled()) return "Off";
    if (paused) return "Paused while recording";
    if (conn != BLE_HS_CONN_HANDLE_NONE) {
        Lock lock;
        return activity.isEmpty() ? String("Connected to the app") : activity;
    }
    if (pairing_active()) return "Pairing";
    if (running) return "Waiting for the app";
    if (paired_count == 0) return "No app paired";
    return "When there is no Wi-Fi";
}

String ble_status_short()
{
    if (conn == BLE_HS_CONN_HANDLE_NONE) return String();
    Lock lock;
    return activity_short.isEmpty() ? String("BT") : activity_short;
}
