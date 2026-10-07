#include "wifi.h"
#include <WiFi.h>
#include <set>
#include "hw/clock.h"
#include "store/config.h"

static volatile bool held = false;
static SemaphoreHandle_t mutex = xSemaphoreCreateMutex();

static bool try_network(const WifiNetwork &net, uint32_t timeout_ms)
{
    Serial.printf("Wi-Fi: trying \"%s\" ...\n", net.ssid.c_str());
    WiFi.begin(net.ssid.c_str(), net.password.c_str());
    uint32_t start = millis();
    while (millis() - start < timeout_ms) {
        wl_status_t st = WiFi.status();
        if (st == WL_CONNECTED) return true;
        if (st == WL_CONNECT_FAILED && millis() - start > 1000) break;  // e.g. wrong password
        delay(100);
    }
    WiFi.disconnect();
    Serial.printf("Wi-Fi: \"%s\" failed\n", net.ssid.c_str());
    return false;
}

// Tries the configured networks in their order and uses the first one that is
// in range and accepts the connection. Hidden networks are tried even when the
// scan doesn't list them.
bool wifi_connect(uint32_t timeout_ms)
{
    if (WiFi.status() == WL_CONNECTED) return true;

    xSemaphoreTake(mutex, portMAX_DELAY);
    bool ok = WiFi.status() == WL_CONNECTED;
    std::vector<WifiNetwork> nets = config_wifi();
    if (!ok && nets.empty()) {
        Serial.println("No Wi-Fi networks configured");
    } else if (!ok) {
        WiFi.mode(WIFI_STA);
        WiFi.setSleep(false);

        std::set<String> visible;
        int found = WiFi.scanNetworks();
        for (int i = 0; i < found; i++) visible.insert(WiFi.SSID(i));
        WiFi.scanDelete();

        for (const WifiNetwork &net : nets) {
            if (net.ssid.isEmpty() || (!net.hidden && !visible.count(net.ssid))) continue;
            if ((ok = try_network(net, timeout_ms))) break;
        }
        if (ok) {
            Serial.printf("Wi-Fi connected to \"%s\", IP %s\n",
                          WiFi.SSID().c_str(), WiFi.localIP().toString().c_str());
            clock_start_ntp();
        } else {
            Serial.printf("Wi-Fi: no configured network available (%d networks in range)\n", found);
        }
    }
    xSemaphoreGive(mutex);
    return ok;
}

bool wifi_connected()
{
    return WiFi.status() == WL_CONNECTED;
}

String wifi_ssid()
{
    return wifi_connected() ? WiFi.SSID() : String();
}

String wifi_ip()
{
    return wifi_connected() ? WiFi.localIP().toString() : String();
}

void wifi_off(bool force)
{
    if (held && !force) return;
    xSemaphoreTake(mutex, portMAX_DELAY);
    if (WiFi.getMode() != WIFI_OFF) {
        WiFi.disconnect(true);
        WiFi.mode(WIFI_OFF);
        Serial.println("Wi-Fi off");
    }
    xSemaphoreGive(mutex);
}

void wifi_hold(bool hold) { held = hold; }
bool wifi_held()          { return held; }
