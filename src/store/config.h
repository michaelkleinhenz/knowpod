#pragma once

#include <Arduino.h>
#include <FS.h>
#include <ArduinoJson.h>
#include <vector>

// Settings live in /config.json on the SD card; the API key in /openrouter.txt.
// A default config.json is created on first boot (importing /wifi.txt if present).
//
// {
//   "wifi": [{"ssid": "...", "password": "...", "hidden": false}, ...]
//                              tried in this order; the first one in range is used
//   "timezone": "CET-1CEST,M3.5.0,M10.5.0/3",        POSIX TZ string
//   "stt": {"model": "openai/whisper-large-v3-turbo", "models": [fallbacks],
//           "language": "", "chunk_minutes": 5},
//   "llm": {"model": "...", "models": [...], "provider": {...}, ...},
//   "model_choices": ["...", "..."],
//   "default_template": "meeting",
//   "speaker_labels": false,
//   "sound_cues": true,        beep when recording starts and stops
//   "sleep_minutes": 5,        idle time before deep sleep (0 = never)
//   "power_off_hours": 12,     time in deep sleep before switching off (0 = never)
//   "processing": "device",    who transcribes and summarizes: "device" (OpenRouter
//                              from the device) or "backend" (the knowpod-service
//                              backend; results are downloaded after the upload)
//   "backend": {"url": "https://www.knowpod.de/api/v1", "token": "",
//               "email": "", "password": ""},
//                              upload recordings to a knowpod-service backend;
//                              the device token comes from POST /devices there
//                              (signed in as the user the recordings belong to).
//                              email/password of that user are only needed with
//                              "processing": "backend" to download the results
//                              (the device token can't read recordings).
//   "web_enabled": true,       web page and MCP server whenever Wi-Fi is available
//   "web_password": ""         generated when web access is first enabled
// }
//
// "llm" is sent as-is as the base of every /chat/completions request, so any
// OpenRouter request option works there: fallback "models", "provider"
// routing, ":nitro"/":floor" variants, "@preset/..." slugs, "reasoning", ...
//
// All functions are thread-safe.

struct WifiNetwork {
    String ssid;
    String password;
    bool   hidden = false;   // not broadcast: try it even if a scan doesn't show it
};

bool config_load(fs::FS &fs);

// Raw config.json for the web editor; config_replace() validates and saves.
String config_json();
bool config_replace(const String &json, String &error);

const String &config_api_key();
std::vector<WifiNetwork> config_wifi();
String config_timezone();
String config_stt_model();
std::vector<String> config_stt_models();   // "model" followed by the "models" fallbacks
String config_stt_language();
int config_stt_chunk_minutes();
void config_llm(JsonDocument &out);     // copy of the "llm" request base
String config_llm_model();
std::vector<String> config_model_choices();
String config_default_template();
bool config_speaker_labels();
bool config_sound_cues();
int config_sleep_minutes();
int config_power_off_hours();
String config_web_password();           // generated and saved if empty
bool config_web_enabled();
String config_backend_url();             // API base, no trailing slash
String config_backend_token();
bool config_backend_enabled();           // url and token set
String config_backend_email();
String config_backend_password();
bool config_processing_backend();        // "processing": "backend"
void config_set_processing_backend(bool backend);

void config_set_llm_model(const String &model);
void config_set_stt_language(const String &language);
void config_set_default_template(const String &name);
void config_set_speaker_labels(bool on);
void config_set_sound_cues(bool on);
void config_set_sleep_minutes(int minutes);
void config_set_web_enabled(bool on);
void config_set_wifi(const std::vector<WifiNetwork> &networks);   // in priority order
