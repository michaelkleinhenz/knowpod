#pragma once

#include <Arduino.h>
#include <FS.h>
#include <ArduinoJson.h>
#include <vector>

// Settings live in /config.json on the SD card. A default config.json is
// created on first boot (importing /wifi.txt if present).
//
// {
//   "wifi": [{"ssid": "...", "password": "...", "hidden": false}, ...]
//                              tried in this order; the first one in range is used
//   "timezone": "CET-1CEST,M3.5.0,M10.5.0/3",        POSIX TZ string
//   "sound_cues": true,        beep when recording starts and stops
//   "sleep_minutes": 5,        idle time before deep sleep (0 = never)
//   "power_off_hours": 12,     time in deep sleep before switching off (0 = never)
//   "backend": {"url": "https://www.knowpod.de/api/v1", "token": ""},
//                              recordings are uploaded to this knowpod-service
//                              backend, which transcribes and summarizes them; the
//                              device token comes from POST /devices there (signed
//                              in as the user the recordings belong to)
//   "web_enabled": true,       local web page whenever Wi-Fi is available
//   "web_password": ""         generated when web access is first enabled
// }
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

std::vector<WifiNetwork> config_wifi();
String config_timezone();
bool config_sound_cues();
int config_sleep_minutes();
int config_power_off_hours();
String config_web_password();           // generated and saved if empty
bool config_web_enabled();
String config_backend_url();             // API base, no trailing slash
String config_backend_token();
bool config_backend_enabled();           // url and token set

void config_set_sound_cues(bool on);
void config_set_sleep_minutes(int minutes);
void config_set_web_enabled(bool on);
void config_set_wifi(const std::vector<WifiNetwork> &networks);   // in priority order
