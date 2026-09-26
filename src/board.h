#pragma once

// Pin map of the Waveshare ESP32-S3-ePaper-3.97
// (see github.com/waveshareteam/ESP32-S3-ePaper-3.97)

// I2C bus shared by the ES8311 codec, TG28 PMIC (AXP2101 compatible),
// PCF85063 RTC, SHTC3 and QMI8658
#define PIN_I2C_SDA   41
#define PIN_I2C_SCL   42

// I2S audio (ES8311)
#define PIN_I2S_MCK   13
#define PIN_I2S_BCK   14
#define PIN_I2S_LRCK  47
#define PIN_I2S_DOUT  48
#define PIN_I2S_DIN   21
#define PIN_PA_CTRL   39   // speaker amplifier enable

// SD card (4-bit SDMMC)
#define PIN_SD_CLK    16
#define PIN_SD_CMD    17
#define PIN_SD_D0     15
#define PIN_SD_D1     7
#define PIN_SD_D2     8
#define PIN_SD_D3     18

// E-paper (SPI)
#define PIN_EPD_SCK   11
#define PIN_EPD_MOSI  12
#define PIN_EPD_CS    10
#define PIN_EPD_DC    9
#define PIN_EPD_RST   46
#define PIN_EPD_BUSY  3

// Buttons. The rocker (up/press/down) and BOOT are active low. The PWR key
// drives the PMIC and is also readable on GPIO1, active high (as in the
// xiaozhi board definition, VBAT_PWR_GPIO).
#define PIN_KEY_UP    4
#define PIN_KEY_OK    5
#define PIN_KEY_DOWN  6
#define PIN_KEY_BOOT  0
#define PIN_KEY_PWR   1
