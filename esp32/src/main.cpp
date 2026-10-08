#include <Arduino.h>
#include <Wire.h>
#include "board.h"
#include "app.h"
#include "debug.h"
#include "audio/audio.h"
#include "hw/buttons.h"
#include "hw/clock.h"
#include "hw/power.h"
#include "proc/worker.h"
#include "store/config.h"
#include "store/recordings.h"
#include "store/sdcard.h"
#include "ui/display.h"

void setup()
{
    Serial.begin(115200);
    // USB serial: when a host has the port open but nobody reads it (e.g. after
    // waking from deep sleep), every print would wait 100 ms for buffer space and
    // the UI would appear frozen. Drop log output instead of waiting.
    Serial.setTxTimeoutMs(0);
    delay(1000);
    Serial.println("\n=== knowpod ===");
    Serial.printf("Reset reason %d, wake-up cause %d\n", (int)esp_reset_reason(), (int)esp_sleep_get_wakeup_cause());

    Wire.begin(PIN_I2C_SDA, PIN_I2C_SCL);
    power_begin();

    // Woken by the timer after a long deep sleep: switch off (on battery)
    if (esp_sleep_get_wakeup_cause() == ESP_SLEEP_WAKEUP_TIMER && !power_status().usb_connected) {
        display_begin();
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
        power_off();
    }

    Serial.println("Init SD card...");
    bool sd_ok = sdcard_begin();
    if (sd_ok) config_load(SDCARD);

    Serial.println("Init clock...");
    clock_begin(config_timezone().c_str());
    if (sd_ok) recordings_begin(SDCARD);
    Serial.println("Init audio...");
    bool codec_ok = audio_begin();
    Serial.println("Init buttons...");
    buttons_begin();

    Serial.println("Init app (display + UI)...");
    app_begin(sd_ok, codec_ok);
    if (sd_ok) worker_begin();
    debug_begin();
    Serial.println("Setup complete.");
}

void loop()
{
    app_loop();
    debug_loop();
    delay(10);
}
