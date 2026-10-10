#pragma once

#include <Arduino.h>

// Thread-safe Wi-Fi control. Connects to the first configured network (in
// config.json order) that is in range, and starts NTP on success.
// `timeout_ms` applies per network.
bool wifi_connect(uint32_t timeout_ms = 10000);
bool wifi_connected();
bool wifi_on();  // the radio is on (connecting, connected or scanning)
String wifi_ssid();
String wifi_ip();

// The radio lock: Wi-Fi starts and stops under it, and so must the Bluetooth stack. On the
// single-core C6 both stacks starting at once can hang the chip.
void wifi_radio_lock();
void wifi_radio_unlock();

// Turns Wi-Fi off unless it is held (e.g. by the web server); `force` ignores holds.
void wifi_off(bool force = false);
void wifi_hold(bool hold);
bool wifi_held();

// Wi-Fi sleeps between the access point's beacons while idle (modem sleep);
// full power keeps the radio awake for the throughput of an upload.
void wifi_full_power(bool on);
