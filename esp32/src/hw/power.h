#pragma once

#include <Arduino.h>

// TG28 power management IC (register compatible with the AXP2101).

struct PowerStatus {
    int      battery_percent;  // -1 if no battery
    uint16_t battery_mv;
    bool     charging;
    bool     usb_connected;
};

bool power_begin();
PowerStatus power_status();

// Cuts power (only effective on battery; USB keeps the board running).
void power_off();
