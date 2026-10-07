#include "config.h"
#include <esp_random.h>

#define CONFIG_PATH      "/config.json"
#define API_KEY_PATH     "/openrouter.txt"
#define LEGACY_WIFI_PATH "/wifi.txt"

static const char DEFAULT_CONFIG[] = R"json({
  "wifi": [],
  "timezone": "CET-1CEST,M3.5.0,M10.5.0/3",
  "stt": {
    "model": "openai/whisper-large-v3-turbo",
    "models": ["openai/whisper-large-v3", "openai/gpt-4o-mini-transcribe"],
    "language": "",
    "chunk_minutes": 5
  },
  "llm": {
    "model": "google/gemini-3.8-flash",
    "models": ["anthropic/claude-sonnet-5"],
    "provider": {"data_collection": "deny"},
    "temperature": 0.3
  },
  "model_choices": [
    "google/gemini-3.8-flash",
    "anthropic/claude-sonnet-5",
    "openai/gpt-5.6-terra",
    "mistralai/mistral-medium-3-5",
    "openrouter/auto"
  ],
  "default_template": "meeting",
  "speaker_labels": false,
  "sound_cues": true,
  "sleep_minutes": 5,
  "power_off_hours": 12,
  "processing": "device",
  "backend": {
    "url": "https://www.knowpod.de/api/v1",
    "token": "",
    "email": "",
    "password": ""
  },
  "web_enabled": true,
  "web_password": ""
})json";

static fs::FS *config_fs = nullptr;
static JsonDocument doc;
static String api_key;
static SemaphoreHandle_t mutex = xSemaphoreCreateRecursiveMutex();

struct Lock {
    Lock()  { xSemaphoreTakeRecursive(mutex, portMAX_DELAY); }
    ~Lock() { xSemaphoreGiveRecursive(mutex); }
};

static bool save_locked()
{
    if (!config_fs) return false;
    File f = config_fs->open(CONFIG_PATH, FILE_WRITE);
    if (!f) {
        Serial.println("Failed to write " CONFIG_PATH);
        return false;
    }
    serializeJsonPretty(doc, f);
    f.close();
    return true;
}

static void import_legacy_wifi(fs::FS &fs)
{
    File f = fs.open(LEGACY_WIFI_PATH, FILE_READ);
    if (!f) return;
    String ssid = f.readStringUntil('\n');
    String pass = f.readStringUntil('\n');
    f.close();
    ssid.trim();
    pass.trim();
    if (ssid.isEmpty()) return;

    JsonObject net = doc["wifi"].add<JsonObject>();
    net["ssid"] = ssid;
    net["password"] = pass;
    Serial.println("Imported Wi-Fi settings from " LEGACY_WIFI_PATH);
}

bool config_load(fs::FS &fs)
{
    Lock lock;
    config_fs = &fs;

    File f = fs.open(CONFIG_PATH, FILE_READ);
    if (f) {
        DeserializationError err = deserializeJson(doc, f);
        f.close();
        if (err) {
            Serial.printf(CONFIG_PATH " is invalid JSON (%s); using defaults\n", err.c_str());
            deserializeJson(doc, DEFAULT_CONFIG);
        }
    } else {
        deserializeJson(doc, DEFAULT_CONFIG);
        import_legacy_wifi(fs);
        if (save_locked())
            Serial.println("Created default " CONFIG_PATH);
    }

    f = fs.open(API_KEY_PATH, FILE_READ);
    if (f) {
        api_key = f.readString();
        api_key.trim();
        f.close();
    }
    if (api_key.isEmpty())
        Serial.println("No OpenRouter API key: put it into " API_KEY_PATH " on the SD card");
    if (doc["wifi"].size() == 0)
        Serial.println("No Wi-Fi configured: add it to \"wifi\" in " CONFIG_PATH);
    return true;
}

String config_json()
{
    Lock lock;
    String s;
    serializeJsonPretty(doc, s);
    return s;
}

bool config_replace(const String &json, String &error)
{
    JsonDocument parsed;
    DeserializationError err = deserializeJson(parsed, json);
    if (err) {
        error = String("Invalid JSON: ") + err.c_str();
        return false;
    }
    if (!parsed.is<JsonObject>()) {
        error = "config.json must be a JSON object";
        return false;
    }
    Lock lock;
    doc = parsed;
    return save_locked();
}

const String &config_api_key() { return api_key; }

std::vector<WifiNetwork> config_wifi()
{
    Lock lock;
    std::vector<WifiNetwork> nets;
    for (JsonObjectConst net : doc["wifi"].as<JsonArrayConst>())
        nets.push_back({net["ssid"] | "", net["password"] | "", net["hidden"] | false});
    return nets;
}

String config_timezone()             { Lock l; return doc["timezone"] | "UTC0"; }
String config_stt_model()            { Lock l; return doc["stt"]["model"] | "openai/whisper-large-v3-turbo"; }
String config_stt_language()         { Lock l; return doc["stt"]["language"] | ""; }
int config_stt_chunk_minutes()       { Lock l; return constrain(doc["stt"]["chunk_minutes"] | 5, 1, 10); }
String config_llm_model()            { Lock l; return doc["llm"]["model"] | "openrouter/auto"; }
String config_default_template()     { Lock l; return doc["default_template"] | "meeting"; }
bool config_speaker_labels()         { Lock l; return doc["speaker_labels"] | false; }
bool config_sound_cues()             { Lock l; return doc["sound_cues"] | true; }
int config_sleep_minutes()           { Lock l; return doc["sleep_minutes"] | 5; }
int config_power_off_hours()         { Lock l; return doc["power_off_hours"] | 12; }
bool config_web_enabled()            { Lock l; return doc["web_enabled"] | true; }
String config_backend_token()        { Lock l; return doc["backend"]["token"] | ""; }
bool config_backend_enabled()        { return !config_backend_url().isEmpty() && !config_backend_token().isEmpty(); }
String config_backend_email()        { Lock l; return doc["backend"]["email"] | ""; }
String config_backend_password()     { Lock l; return doc["backend"]["password"] | ""; }
bool config_processing_backend()     { Lock l; return doc["processing"] == "backend"; }
void config_set_processing_backend(bool backend) { Lock l; doc["processing"] = backend ? "backend" : "device"; save_locked(); }

String config_backend_url()
{
    Lock lock;
    String url = doc["backend"]["url"] | "https://www.knowpod.de/api/v1";
    url.trim();
    while (url.endsWith("/")) url.remove(url.length() - 1);
    return url;
}

std::vector<String> config_stt_models()
{
    Lock lock;
    std::vector<String> models = {doc["stt"]["model"] | "openai/whisper-large-v3-turbo"};
    // Older config files have no "models" key: use the default fallbacks then
    JsonArrayConst fallbacks = doc["stt"]["models"];
    if (fallbacks.isNull()) {
        for (const char *m : {"openai/whisper-large-v3", "openai/gpt-4o-mini-transcribe"})
            if (models[0] != m) models.push_back(m);
    }
    for (const char *m : fallbacks)
        if (m && models[0] != m) models.push_back(m);
    return models;
}

void config_llm(JsonDocument &out)
{
    Lock lock;
    out.set(doc["llm"]);
}

std::vector<String> config_model_choices()
{
    Lock lock;
    std::vector<String> models;
    for (const char *m : doc["model_choices"].as<JsonArrayConst>())
        if (m) models.push_back(m);
    return models;
}

String config_web_password()
{
    Lock lock;
    String pw = doc["web_password"] | "";
    if (pw.isEmpty()) {
        const char *alphabet = "abcdefghjkmnpqrstuvwxyz23456789";
        for (int i = 0; i < 10; i++) pw += alphabet[esp_random() % strlen(alphabet)];
        doc["web_password"] = pw;
        save_locked();
    }
    return pw;
}

void config_set_llm_model(const String &model)       { Lock l; doc["llm"]["model"] = model; save_locked(); }
void config_set_stt_language(const String &language) { Lock l; doc["stt"]["language"] = language; save_locked(); }
void config_set_default_template(const String &name) { Lock l; doc["default_template"] = name; save_locked(); }
void config_set_speaker_labels(bool on)              { Lock l; doc["speaker_labels"] = on; save_locked(); }
void config_set_sound_cues(bool on)                 { Lock l; doc["sound_cues"] = on; save_locked(); }
void config_set_sleep_minutes(int minutes)           { Lock l; doc["sleep_minutes"] = minutes; save_locked(); }
void config_set_wifi(const std::vector<WifiNetwork> &networks)
{
    Lock lock;
    JsonArray list = doc["wifi"].to<JsonArray>();
    for (const WifiNetwork &n : networks) {
        JsonObject o = list.add<JsonObject>();
        o["ssid"] = n.ssid;
        o["password"] = n.password;
        if (n.hidden) o["hidden"] = true;
    }
    save_locked();
}

void config_set_web_enabled(bool on)                 { Lock l; doc["web_enabled"] = on; save_locked(); }
