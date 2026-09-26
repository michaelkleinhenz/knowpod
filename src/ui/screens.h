#pragma once

#include <Arduino.h>
#include <functional>
#include <vector>
#include "ui/ui.h"

Screen *make_home();
Screen *make_recordings();
Screen *make_detail(const String &id);
Screen *make_settings();
Screen *make_recording();                       // recording in progress
Screen *make_ask(const String &scope_id);       // empty scope: all recordings
Screen *make_sleep();                           // shown during deep sleep

// With `home_after_ms`, the message returns to the home screen by itself.
Screen *make_message(const String &title, const String &body, bool markdown = false,
                     uint32_t home_after_ms = 0);

// A list of options; `on_select` runs after the menu closed itself.
Screen *make_menu(const String &title, const std::vector<String> &options, int selected,
                  std::function<void(int)> on_select);

// Press confirms, back cancels; `on_confirm` runs after the screen closed.
Screen *make_confirm(const String &title, const String &body, std::function<void()> on_confirm);

// Shows a message immediately, before a blocking operation.
void show_progress(const String &title, const String &body);
