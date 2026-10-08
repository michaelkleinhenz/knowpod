#include "app.h"
#include <driver/rtc_io.h>
#include "audio/audio.h"
#include "board.h"
#include "debug.h"
#include "hw/buttons.h"
#include "hw/clock.h"
#include "hw/power.h"
#include "net/ble.h"
#include "net/web.h"
#include "net/wifi.h"
#include "proc/worker.h"
#include "session.h"
#include "store/config.h"
#include "store/recordings.h"
#include "ui/screens.h"

// 3.97" buttons (HAS_ROCKER):
//   rocker up/down  scroll             rocker press  open/choose
//   hold rocker     back               BOOT click    back (highlight while recording)
//   hold BOOT       start/stop recording             PWR click   sleep
//   hold PWR        power off
//
// 1.54G buttons (BOOT only — PWR is hardware-only via ETA6098):
//   BOOT click      next / highlight while recording
//   BOOT hold       select (OK) / stop recording
//   BOOT hold 2s    back (sleep on home screen)

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
    bool ready = !config_wifi().empty() &&
                 (config_processing_backend() ? config_backend_enabled() : !config_api_key().isEmpty());
    // Without Wi-Fi, a paired knowpod app takes the recording over Bluetooth and uploads it
    if (config_processing_backend() && config_backend_enabled() && config_bluetooth_enabled() &&
        ble_paired_count() > 0)
        ready = true;
    body += !ready ? String(config_processing_backend()
                                ? "Add Wi-Fi and the backend settings to get a transcript and summary."
                                : "Add Wi-Fi and an OpenRouter key to get a transcript and summary.")
                   : String(config_processing_backend()
                                ? "It is uploaded; the backend's transcript and summary follow."
                                : "Transcript and summary will be ready when Wi-Fi is available.");

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
#if SCREEN_H >= 400
    draw_logo(SCREEN_W / 2, SCREEN_H / 2 - 50, 72);
    draw_text_centered(SCREEN_H / 2 + 20, "Powered off", FONT_BODY);
    draw_text_centered(SCREEN_H / 2 + 60, "Hold PWR to switch on", FONT_SMALL);
#else
    draw_logo(SCREEN_W / 2, SCREEN_H / 2 - 20, 28);
    draw_text_centered(SCREEN_H / 2 + 14, "Powered off", FONT_BODY);
    draw_text_centered(SCREEN_H / 2 + 30, "Hold PWR to turn on", FONT_SMALL);
#endif
    display_update(true);
    web_stop();
    power_off();
    ui_dirty(true);
}

// Deep sleep keeps the e-ink image; a button or timer wake the board, which
// then boots again. A timer switches the device off after a long sleep.
static void enter_deep_sleep()
{
    Serial.println("Going to deep sleep");
    ui_push(make_sleep());
    ui_render(true);
    wifi_off(true);

    esp_sleep_pd_config(ESP_PD_DOMAIN_RTC_PERIPH, ESP_PD_OPTION_ON);

#ifdef HAS_ROCKER
    static const gpio_num_t WAKE_PINS[] = {
        (gpio_num_t)PIN_KEY_UP, (gpio_num_t)PIN_KEY_OK, (gpio_num_t)PIN_KEY_DOWN};
    uint64_t mask = 0;
    for (gpio_num_t pin : WAKE_PINS) {
        rtc_gpio_pullup_en(pin);
        rtc_gpio_pulldown_dis(pin);
        mask |= 1ULL << pin;
    }
    esp_sleep_enable_ext1_wakeup(mask, ESP_EXT1_WAKEUP_ANY_LOW);
    rtc_gpio_pulldown_en((gpio_num_t)PIN_KEY_PWR);
    rtc_gpio_pullup_dis((gpio_num_t)PIN_KEY_PWR);
    esp_sleep_enable_ext0_wakeup((gpio_num_t)PIN_KEY_PWR, 1);
#else
    // 1.54G: BOOT (GPIO0) wakes via ext1 (active low)
    rtc_gpio_pullup_en((gpio_num_t)PIN_KEY_BOOT);
    rtc_gpio_pulldown_dis((gpio_num_t)PIN_KEY_BOOT);
    esp_sleep_enable_ext1_wakeup(1ULL << PIN_KEY_BOOT, ESP_EXT1_WAKEUP_ANY_LOW);
#endif

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
    // An app reading recordings over Bluetooth, or one being paired, needs the device awake
    if (ble_connected() || ble_pairing()) return false;
    return !session_active() && !recorder_active() && !debug_busy() && !worker_busy();
}

// ============================================================
// Events
// ============================================================

static void handle_event(const ButtonEvent &ev)
{
    Serial.printf("Button %s %s\n", button_name(ev.id),
                  ev.action == BTN_CLICK ? "click" : ev.action == BTN_LONG ? "long"
                  : ev.action == BTN_VLONG ? "vlong" : "repeat");
    last_activity = millis();

#ifdef HAS_ROCKER
    // ── 3.97" (5 buttons) ───────────────────────────────────────
    if (ev.id == BTN_PWR) {
        if (ev.action == BTN_LONG) power_off_now();
        else if (!session_active() && !recorder_active()) enter_deep_sleep();
        return;
    }

    if (session_active()) {
        if (ev.id == BTN_BOOT && ev.action == BTN_CLICK) { session_highlight(); ui_dirty(); }
        else if (ev.id == BTN_BOOT && ev.action == BTN_LONG) stop_recording();
        return;
    }

    if (ev.id == BTN_BOOT && ev.action == BTN_LONG) {
        if (!recorder_active()) start_recording_from_ui();
        return;
    }

    Screen *top = ui_top();
    if (!top) return;
    bool back = (ev.id == BTN_BOOT && ev.action == BTN_CLICK) || (ev.id == BTN_OK && ev.action == BTN_LONG);
    if (back) top->on_back();
    else top->on_button(ev);

#else
    // ── 1.54G (single button: BOOT) ────────────────────────────
    // click = next/highlight, hold = select/stop, hold 2s = back/sleep

    if (session_active()) {
        if (ev.id == BTN_BOOT && ev.action == BTN_CLICK) { session_highlight(); ui_dirty(); }
        else if (ev.id == BTN_BOOT && ev.action == BTN_LONG) stop_recording();
        return;
    }

    if (ev.id == BTN_BOOT && ev.action == BTN_VLONG) {
        if (ui_is_root()) enter_deep_sleep();
        else if (Screen *top = ui_top()) top->on_back();
        return;
    }

    Screen *top = ui_top();
    if (!top) return;

    if (ev.id == BTN_BOOT && ev.action == BTN_CLICK) {
        top->on_button({BTN_DOWN, BTN_CLICK});
    } else if (ev.id == BTN_BOOT && ev.action == BTN_LONG) {
        top->on_button({BTN_OK, BTN_CLICK});
    }
#endif
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
