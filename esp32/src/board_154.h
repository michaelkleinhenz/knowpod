#pragma once

// Pin map of the Waveshare ESP32-C6-ePaper-1.54
// (see github.com/waveshareteam/ESP32-C6-ePaper-1.54)
// SoC: ESP32-C6 (single core, 512 KB SRAM, no PSRAM), 16 MB flash

// I2C bus shared by the ES8311 codec, TCA9554 I/O expander, PCF85063 RTC
// and SHTC3
#define PIN_I2C_SDA   18
#define PIN_I2C_SCL   8

// I2S audio (ES8311); the NS4150B speaker amplifier is enabled through
// EXIO_PA_CTRL
// Recordings are encoded to MP3 while recording (lib/shine): ~14 MB per hour
// instead of ~115 MB of WAV
#define REC_MP3           1
#define MP3_BITRATE_KBPS  32
#define PIN_I2S_MCK   19
#define PIN_I2S_BCK   21
#define PIN_I2S_LRCK  22
#define PIN_I2S_DOUT  23
#define PIN_I2S_DIN   20

// SPI bus shared by the e-paper and the SD card
#define PIN_SPI_SCK   6
#define PIN_SPI_MOSI  5
#define PIN_SPI_MISO  4

// SD card (SPI mode; the C6 has no SDMMC host)
#define PIN_SD_CS     3
#define SD_SPI        1

// E-paper (1.54" V2 200x200 black/white, SSD1681 controller)
#define PIN_EPD_SCK   PIN_SPI_SCK
#define PIN_EPD_MOSI  PIN_SPI_MOSI
#define PIN_EPD_CS    7
#define PIN_EPD_DC    15
#define PIN_EPD_RST   11
#define PIN_EPD_BUSY  10

// TCA9554 I/O expander (I2C 0x20) outputs, all active high
#define EXIO_ADDR       0x20
#define EXIO_EPD_PWR    0   // e-paper supply
#define EXIO_AUDIO_PWR  1   // codec and speaker amplifier supply
#define EXIO_PA_CTRL    3   // speaker amplifier enable (NS4150B CTRL)
#define EXIO_LED        4
#define EXIO_VBAT_HOLD  5   // keeps the board on when running from the battery

// Battery voltage through a 1:2 divider (VBAT = VADC * 2)
#define PIN_BAT_ADC   0

// Buttons, both active low. PWR also switches the battery supply on in
// hardware; GPIO2 is an LP GPIO and can wake the chip from deep sleep,
// BOOT (GPIO9) cannot.
#define PIN_KEY_BOOT  9
#define PIN_KEY_PWR   2

// Core that runs loop(); tasks that must keep up with it are pinned here
#define LOOP_CORE  0
