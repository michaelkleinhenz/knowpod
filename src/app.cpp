#include "app.h"
#include <driver/rtc_io.h>
#include "audio/audio.h"
#include "board.h"
#include "debug.h"
#include "hw/buttons.h"
#include "hw/clock.h"
#include "hw/power.h"
#include "net/web.h"
#include "net/wifi.h"
#include "proc/worker.h"
#include "session.h"
#include "store/config.h"
#include "store/recordings.h"
#include "ui/screens.h"

// Buttons:
//   rocker up/down  move selection / scroll
//   rocker press    open / choose        hold rocker  back
//   BOOT click      back (highlight while recording)
//   hold BOOT       start/stop recording (from any screen)
//   PWR click       sleep (PWR or rocker wakes)   hold PWR  power off

#define SAVED_HOME_MS  10000   // "Saved" message returns to the home screen

static bool sd_ok, codec_ok;
static int last_minute = -1;
static uint32_t last_activity = 0;

// ============================================================
// Recording
// ============================================================

void start_recording_from_ui()
{
    String error;
    if (!sd_ok) error = "No SD card.";
    else if (!codec_ok) error = "Audio codec not available.";
    else if (debug_busy() || recorder_active()) error = "The microphone is busy.";
    if (error.isEmpty() && session_start(error)) {
        ui_push(make_recording());
        return;
    }
    ui_push(make_message("Cannot record", error));
}

static void stop_recording(const String &reason = String())
{
    SessionInfo info;
    session_stop(info);

    String body;
    if (!reason.isEmpty()) body += reason + "\n\n";
    body += "Duration: " + format_duration(info.seconds) + "\n";
    body += "Highlights: " + String(info.highlights) + "\n\n";
    body += config_api_key().isEmpty() || config_wifi().empty()
                ? "Add Wi-Fi and an OpenRouter key to get a transcript and summary."
                : "Transcript and summary will be ready when Wi-Fi is available.";

    ui_replace(make_message(reason.isEmpty() ? "Saved" : "Recording stopped", body, false, SAVED_HOME_MS));
    ui_dirty(true);  // the recording screen changed many times
}

// ============================================================
// Power
// ============================================================

static void power_off_now()
{
    if (session_active()) stop_recording();
    display_clear();
    draw_logo(SCREEN_W / 2, SCREEN_H / 2 - 50, 72);
    draw_text_centered(SCREEN_H / 2 + 20, "Powered off", FONT_BODY);
    draw_text_centered(SCREEN_H / 2 + 60, "Hold PWR to switch on", FONT_SMALL);
    display_update(true);
    web_stop();
    power_off();  // no effect on USB power
    ui_dirty(true);
}

static const gpio_num_t WAKE_PINS[] = {(gpio_num_t)PIN_KEY_UP, (gpio_num_t)PIN_KEY_OK, (gpio_num_t)PIN_KEY_DOWN};

// Deep sleep keeps the e-ink image; PWR (GPIO1, active high, ext0) or the
// rocker (active low, ext1) wake the board, which then boots again. BOOT is
// not used for waking since it is a strapping pin. A timer switches the
// device off after a longer time in sleep.
static void enter_deep_sleep()
{
    Serial.println("Going to deep sleep");
    ui_push(make_sleep());
    ui_render(true);
    wifi_off(true);

    uint64_t mask = 0;
    for (gpio_num_t pin : WAKE_PINS) {
        rtc_gpio_pullup_en(pin);
        rtc_gpio_pulldown_dis(pin);
        mask |= 1ULL << pin;
    }
    rtc_gpio_pulldown_en((gpio_num_t)PIN_KEY_PWR);
    rtc_gpio_pullup_dis((gpio_num_t)PIN_KEY_PWR);
    esp_sleep_pd_config(ESP_PD_DOMAIN_RTC_PERIPH, ESP_PD_OPTION_ON);  // keep the pulls
    esp_sleep_enable_ext1_wakeup(mask, ESP_EXT1_WAKEUP_ANY_LOW);
    esp_sleep_enable_ext0_wakeup((gpio_num_t)PIN_KEY_PWR, 1);
    int hours = config_power_off_hours();
    if (hours > 0) esp_sleep_enable_timer_wakeup((uint64_t)hours * 3600ULL * 1000000ULL);
    Serial.flush();
    esp_deep_sleep_start();
}

static bool may_sleep()
{
    int minutes = config_sleep_minutes();
    if (minutes <= 0 || millis() - last_activity < (uint32_t)minutes * 60000) return false;
    // With web access on USB power, stay awake so the web page and MCP stay reachable
    if (web_active() && power_status().usb_connected) return false;
    return !session_active() && !recorder_active() && !debug_busy() && !worker_busy();
}

// ============================================================
// Events
// ============================================================

static void handle_event(const ButtonEvent &ev)
{
    Serial.printf("Button %s %s\n", button_name(ev.id),
                  ev.action == BTN_CLICK ? "click" : ev.action == BTN_LONG ? "long" : "repeat");
    last_activity = millis();

    if (ev.id == BTN_PWR) {
        if (ev.action == BTN_LONG) power_off_now();
        else if (!session_active() && !recorder_active()) enter_deep_sleep();  // never interrupts a recording
        return;
    }

    if (session_active()) {
        // Only BOOT works while recording, so bumping the rocker changes nothing
        if (ev.id == BTN_BOOT && ev.action == BTN_CLICK) {
            session_highlight();
            ui_dirty();
        } else if (ev.id == BTN_BOOT && ev.action == BTN_LONG) {
            stop_recording();
        }
        return;
    }

    if (ev.id == BTN_BOOT && ev.action == BTN_LONG) {
        if (!recorder_active()) start_recording_from_ui();  // not while asking a question
        return;
    }

    Screen *top = ui_top();
    if (!top) return;
    bool back = (ev.id == BTN_BOOT && ev.action == BTN_CLICK) || (ev.id == BTN_OK && ev.action == BTN_LONG);
    if (back) top->on_back();
    else top->on_button(ev);
}

// ============================================================
// Entry points
// ============================================================

void app_begin(bool sd, bool codec)
{
    sd_ok = sd;
    codec_ok = codec;
    last_activity = millis();
    display_begin();
    ui_push(make_home());
    ui_render(true);
}

void app_loop()
{
    ui_collect_garbage();
    clock_poll();

    if (session_active()) {
        String reason;
        if (!session_poll(reason)) stop_recording(reason);
    }

    ButtonEvent ev;
    while (buttons_get(ev)) handle_event(ev);

    web_poll();
    if (Screen *top = ui_top()) top->tick();

    // Keep the clock in the status bar current
    int minute = clock_valid() ? clock_format("%M").toInt() : -1;
    if (minute != last_minute) {
        last_minute = minute;
        ui_dirty();
    }

    if (may_sleep()) enter_deep_sleep();
    ui_render();
}
