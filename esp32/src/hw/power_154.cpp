#ifdef BOARD_EPAPER_154

#include "power.h"
#include "board.h"
#include "exio.h"
#include <driver/rtc_io.h>

// No PMIC: the PWR key switches the battery supply on in hardware and the
// firmware keeps it on through the expander's VBAT hold output. Battery
// voltage is read through a 1:2 divider on PIN_BAT_ADC.

static uint16_t read_battery_mv()
{
    return analogReadMilliVolts(PIN_BAT_ADC) * 2;  // calibrated ADC reading
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
    PowerStatus s = {-1, 0, false, false};
    uint16_t mv = read_battery_mv();
    if (mv > 2500) {
        s.battery_mv = mv;
        s.battery_percent = voltage_to_percent(mv);
        // No charger status line; a voltage above a resting cell's means USB
        s.charging = mv > 4250;
        s.usb_connected = mv > 4250;
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
