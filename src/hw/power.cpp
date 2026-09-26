#include "power.h"
#include "board.h"
#include <Wire.h>

#define XPOWERS_CHIP_AXP2101
#include <XPowersLib.h>

static XPowersPMU pmu;
static bool pmu_ok = false;

bool power_begin()
{
    pmu_ok = pmu.begin(Wire, AXP2101_SLAVE_ADDRESS, PIN_I2C_SDA, PIN_I2C_SCL);
    if (!pmu_ok) {
        Serial.println("PMIC init FAILED");
        return false;
    }

    // Same configuration as Waveshare's factory firmware (axp_prot.cpp)
    pmu.setVbusVoltageLimit(XPOWERS_AXP2101_VBUS_VOL_LIM_4V36);
    pmu.setVbusCurrentLimit(XPOWERS_AXP2101_VBUS_CUR_LIM_1500MA);
    pmu.setSysPowerDownVoltage(2600);
    pmu.setDC1Voltage(3300);
    pmu.setALDO1Voltage(3300);
    pmu.setALDO2Voltage(3300);
    pmu.setALDO3Voltage(3300);

    pmu.setPowerKeyPressOffTime(XPOWERS_POWEROFF_4S);
    pmu.setPowerKeyPressOnTime(XPOWERS_POWERON_1S);

    pmu.enableTemperatureMeasure();
    pmu.enableBattDetection();
    pmu.enableVbusVoltageMeasure();
    pmu.enableBattVoltageMeasure();
    pmu.enableSystemVoltageMeasure();

    pmu.setChargingLedMode(XPOWERS_CHG_LED_OFF);
    pmu.setPrechargeCurr(XPOWERS_AXP2101_PRECHARGE_50MA);
    pmu.setChargerConstantCurr(XPOWERS_AXP2101_CHG_CUR_200MA);
    pmu.setChargerTerminationCurr(XPOWERS_AXP2101_CHG_ITERM_25MA);
    pmu.setChargeTargetVoltage(XPOWERS_AXP2101_CHG_VOL_4V2);

    // RTC backup battery
    pmu.setButtonBatteryChargeVoltage(3000);
    pmu.enableButtonBatteryCharge();

    pmu.setLowBatWarnThreshold(10);
    pmu.setLowBatShutdownThreshold(5);

    // The PWR key is read on GPIO1 (see buttons.cpp); no PMIC interrupts needed
    pmu.disableIRQ(XPOWERS_AXP2101_ALL_IRQ);
    pmu.clearIrqStatus();

    PowerStatus s = power_status();
    Serial.printf("PMIC: battery %d%% (%u mV)%s%s\n", s.battery_percent, s.battery_mv,
                  s.charging ? ", charging" : "", s.usb_connected ? ", USB" : "");
    return true;
}

PowerStatus power_status()
{
    PowerStatus s = {-1, 0, false, false};
    if (!pmu_ok) return s;
    if (pmu.isBatteryConnect()) {
        s.battery_percent = pmu.getBatteryPercent();
        s.battery_mv = pmu.getBattVoltage();
    }
    s.charging = pmu.isCharging();
    s.usb_connected = pmu.isVbusIn();
    return s;
}

void power_off()
{
    if (pmu_ok) pmu.shutdown();
}
