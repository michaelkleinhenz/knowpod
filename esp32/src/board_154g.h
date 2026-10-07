#pragma once

// Pin map of the Waveshare ESP32-S3-ePaper-1.54G
// (see github.com/waveshareteam/ESP32-S3-ePaper-1.54G)
// SoC: ESP32-S3-PICO-1-N8R8 (8 MB flash, 8 MB PSRAM)

// I2C bus shared by the ES8311 codec, PCF85063 RTC and SHTC3
#define PIN_I2C_SDA   47
#define PIN_I2C_SCL   48

// I2S audio (ES8311)
#define PIN_I2S_MCK   14
#define PIN_I2S_BCK   15
#define PIN_I2S_LRCK  38
#define PIN_I2S_DOUT  16
#define PIN_I2S_DIN   45
#define PIN_PA_EN     42   // ES8311 power supply enable
#define PIN_PA_CTRL   46   // speaker amplifier enable

// SD card (1-bit SDIO)
#define PIN_SD_CLK    39
#define PIN_SD_CMD    41
#define PIN_SD_D0     40

// E-paper (SPI, UC8253 controller, 200x200 4-color)
#define PIN_EPD_SCK   12
#define PIN_EPD_MOSI  13
#define PIN_EPD_CS    11
#define PIN_EPD_DC    10
#define PIN_EPD_RST   9
#define PIN_EPD_BUSY  8
#define PIN_EPD_PWR   6    // e-paper 3.3 V supply enable (MP1605)

// Battery (ETA6098 charger, no I2C PMIC)
#define PIN_BAT_ADC   4    // voltage divider 200 K / 200 K → VBAT = VADC * 2
#define PIN_BAT_CTRL  17
#define PIN_BAT_KEY   18   // battery power enable

// RTC interrupt
#define PIN_RTC_INT   5

// Buttons. Only BOOT is available as a regular GPIO button.
// PWR controls the ETA6098 directly; GPIO18 can detect battery power state.
#define PIN_KEY_BOOT  0
#define PIN_KEY_PWR   18
