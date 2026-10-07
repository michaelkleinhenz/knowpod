#pragma once

#include <Arduino.h>

// Wall clock: the PCF85063 RTC keeps UTC across power cycles, the system
// clock is set from it at boot, and NTP corrects both when Wi-Fi is up.

bool clock_begin(const char *posix_tz);

// Starts NTP; call after Wi-Fi connects.
void clock_start_ntp();

// Call regularly from the main loop; writes NTP time to the RTC once synced.
void clock_poll();

// True once the time came from a set RTC or from NTP.
bool clock_valid();

// strftime() on local time; returns "--:--"-style placeholder if invalid.
String clock_format(const char *fmt);
