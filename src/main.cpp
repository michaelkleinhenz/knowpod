#include <Arduino.h>
#include <Wire.h>
#include <SD_MMC.h>
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
#include "store/templates.h"
#include "ui/display.h"

static bool init_sd_card()
{
    SD_MMC.setPins(PIN_SD_CLK, PIN_SD_CMD, PIN_SD_D0, PIN_SD_D1, PIN_SD_D2, PIN_SD_D3);
    if (!SD_MMC.begin("/sdcard", true)) {
        Serial.println("SD card mount FAILED");
        return false;
    }
    Serial.printf("SD card: %llu MB total, %llu MB used\n",
                  SD_MMC.totalBytes() / (1024 * 1024), SD_MMC.usedBytes() / (1024 * 1024));
    return true;
}

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
        draw_logo(SCREEN_W / 2, SCREEN_H / 2 - 50, 72);
        draw_text_centered(SCREEN_H / 2 + 20, "Powered off", FONT_BODY);
        draw_text_centered(SCREEN_H / 2 + 60, "Hold PWR to switch on", FONT_SMALL);
        display_update(true);
        power_off();
    }

    bool sd_ok = init_sd_card();
    if (sd_ok) config_load(SD_MMC);

    clock_begin(config_timezone().c_str());
    if (sd_ok) {
        recordings_begin(SD_MMC);  // needs the clock for new ids
        templates_begin(SD_MMC);
    }
    bool codec_ok = audio_begin();
    buttons_begin();

    app_begin(sd_ok, codec_ok);
    if (sd_ok) worker_begin();
    debug_begin();
}

void loop()
{
    app_loop();
    debug_loop();
    delay(10);
}
