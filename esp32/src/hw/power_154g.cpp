#ifdef BOARD_EPAPER_154G

#include "power.h"
#include "board.h"
#include <esp_adc/adc_oneshot.h>

// ETA6098 charger with no I2C interface. Battery voltage is read through
// a 200 K / 200 K divider on PIN_BAT_ADC → VBAT = VADC × 2.

static adc_oneshot_unit_handle_t adc_handle = nullptr;
static bool adc_ok = false;

static uint16_t read_battery_mv()
{
    if (!adc_ok) return 0;
    int raw = 0;
    adc_oneshot_read(adc_handle, ADC_CHANNEL_3, &raw);  // GPIO4 = ADC1_CH3
    // ESP32-S3 ADC: 12-bit, default 0–3.1 V attenuation range (ADC_ATTEN_DB_12)
    uint16_t adc_mv = (uint16_t)(raw * 3100 / 4095);
    return adc_mv * 2;  // voltage divider
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
    adc_oneshot_unit_init_cfg_t unit_cfg = {
        .unit_id = ADC_UNIT_1,
    };
    if (adc_oneshot_new_unit(&unit_cfg, &adc_handle) != ESP_OK) {
        Serial.println("ADC unit init FAILED");
        return false;
    }
    adc_oneshot_chan_cfg_t chan_cfg = {
        .atten = ADC_ATTEN_DB_12,
        .bitwidth = ADC_BITWIDTH_12,
    };
    if (adc_oneshot_config_channel(adc_handle, ADC_CHANNEL_3, &chan_cfg) != ESP_OK) {
        Serial.println("ADC channel config FAILED");
        return false;
    }
    adc_ok = true;

    pinMode(PIN_BAT_KEY, OUTPUT);
    digitalWrite(PIN_BAT_KEY, HIGH);

    PowerStatus s = power_status();
    Serial.printf("Battery: %d%% (%u mV)\n", s.battery_percent, s.battery_mv);
    return true;
}

PowerStatus power_status()
{
    PowerStatus s = {-1, 0, false, false};
    uint16_t mv = read_battery_mv();
    if (mv > 2500) {
        s.battery_mv = mv;
        s.battery_percent = voltage_to_percent(mv);
        s.charging = mv > 4250;
        s.usb_connected = mv > 4250;
    }
    return s;
}

void power_off()
{
    digitalWrite(PIN_BAT_KEY, LOW);
    delay(100);
    esp_deep_sleep_start();
}

#endif // BOARD_EPAPER_154G
