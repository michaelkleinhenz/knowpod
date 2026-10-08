#pragma once

#include <Arduino.h>

// TCA9554 I/O expander on the 1.54" board: switches the e-paper and audio
// supplies, the LED and the battery hold. Wire must be started.

bool exio_begin();
void exio_set(uint8_t pin, bool level);

// Reads the expander's output and direction registers back (diagnostics).
bool exio_read(uint8_t &outputs, uint8_t &config);
