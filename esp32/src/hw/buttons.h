#pragma once

#include <Arduino.h>

enum ButtonId : uint8_t {
    BTN_UP,     // rocker up
    BTN_DOWN,   // rocker down
    BTN_OK,     // rocker press
    BTN_BOOT,
    BTN_PWR,    // PMIC power key
};

enum ButtonAction : uint8_t {
    BTN_CLICK,
    BTN_LONG,    // held for BTN_LONG_MS (fires while still held, or on release for vlong buttons)
    BTN_VLONG,   // held for BTN_VLONG_MS (fires while still held; only for vlong-capable buttons)
    BTN_REPEAT,  // up/down held: auto-repeat
};

struct ButtonEvent {
    ButtonId     id;
    ButtonAction action;
};

#define BTN_LONG_MS    800
#define BTN_VLONG_MS   2000
#define BTN_REPEAT_MS  250

void buttons_begin();
bool buttons_get(ButtonEvent &ev, TickType_t wait = 0);
void buttons_post(ButtonEvent ev);
const char *button_name(ButtonId id);
