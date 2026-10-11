#ifdef BOARD_EPAPER_154

#include "power.h"
#include "board.h"
#include "exio.h"
#include <driver/rtc_io.h>

// No PMIC: the PWR key switches the battery supply on in hardware and the
// firmware keeps it on through the expander's VBAT hold output. Battery
// voltage is read through a 1:2 divider on PIN_BAT_ADC.
//
// A single ADC reading is noisy (tens of mV, doubled by the divider) and the
// cell voltage sags while the radio or the e-paper draw current, so the
// percentage would jump around. Readings are averaged over many samples, smoothed
// over time, and the shown percentage only follows clear changes.

#define BAT_SAMPLES       32
#define BAT_INTERVAL_MS   5000   // fresh reading at most this often
#define BAT_HYSTERESIS    3      // percentage points

static uint16_t read_battery_mv()
{
    uint32_t sum = 0;
    for (int i = 0; i < BAT_SAMPLES; i++) sum += analogReadMilliVolts(PIN_BAT_ADC);  // calibrated
    return sum * 2 / BAT_SAMPLES;
}

static int voltage_to_percent(uint16_t mv)
{
    if (mv >= 4150) return 100;
    if (mv >= 4050) return 80 + (mv - 4050) * 20 / 100;
    if (mv >= 3850) return 50 + (mv - 3850) * 30 / 200;
    if (mv >= 3700) return 20 + (mv - 3700) * 30 / 150;
    if (mv >= 3500) return 5 + (mv - 3500) * 15 / 200;
    if (mv >= 3300) return (mv - 3300) * 5 / 200;
    return 0;
}

bool power_begin()
{
    bool ok = exio_begin();

    PowerStatus s = power_status();
    Serial.printf("Battery: %d%% (%u mV)\n", s.battery_percent, s.battery_mv);
    return ok;
}

PowerStatus power_status()
{
    static uint32_t last_read;
    static bool have_reading = false;
    static uint32_t smooth_mv;  // smoothed voltage
    static int shown_percent;

    uint32_t now = millis();
    if (!have_reading || now - last_read >= BAT_INTERVAL_MS) {
        uint16_t mv = read_battery_mv();
        // Start over on the first reading and after a big jump (USB plugged in or out)
        if (!have_reading || abs((int)mv - (int)smooth_mv) > 300) {
            smooth_mv = mv;
            shown_percent = voltage_to_percent(mv);
        } else {
            smooth_mv = (smooth_mv * 3 + mv) / 4;
            int p = voltage_to_percent(smooth_mv);
            if (abs(p - shown_percent) >= BAT_HYSTERESIS || p == 0 || p == 100) shown_percent = p;
        }
        have_reading = true;
        last_read = now;
    }

    PowerStatus s = {-1, 0, false, false};
    if (smooth_mv > 2500) {
        s.battery_mv = smooth_mv;
        s.battery_percent = shown_percent;
        // No charger status line; a voltage above a resting cell's means USB
        s.charging = smooth_mv > 4250;
        s.usb_connected = smooth_mv > 4250;
    }
    return s;
}

void power_off()
{
    exio_set(EXIO_VBAT_HOLD, false);
    delay(100);
    // Still running: USB power. Sleep until PWR is pressed again.
    pinMode(PIN_KEY_PWR, INPUT_PULLUP);
    while (digitalRead(PIN_KEY_PWR) == LOW) delay(10);
    rtc_gpio_pullup_en((gpio_num_t)PIN_KEY_PWR);
    rtc_gpio_pulldown_dis((gpio_num_t)PIN_KEY_PWR);
    esp_sleep_enable_ext1_wakeup(1ULL << PIN_KEY_PWR, ESP_EXT1_WAKEUP_ANY_LOW);
    esp_deep_sleep_start();
}

#endif // BOARD_EPAPER_154
