#include "sdcard.h"

#ifdef SD_SPI
#include <SPI.h>
#define SD_SPI_FREQ  20000000
#endif

bool sdcard_begin()
{
#if defined(SD_SPI)
    pinMode(PIN_EPD_CS, OUTPUT);  // keep the e-paper off the shared bus
    digitalWrite(PIN_EPD_CS, HIGH);
    SPI.begin(PIN_SPI_SCK, PIN_SPI_MISO, PIN_SPI_MOSI, -1);
    bool ok = SD.begin(PIN_SD_CS, SPI, SD_SPI_FREQ, "/sdcard");
#else
#ifdef SD_4BIT
    SD_MMC.setPins(PIN_SD_CLK, PIN_SD_CMD, PIN_SD_D0, PIN_SD_D1, PIN_SD_D2, PIN_SD_D3);
#else
    SD_MMC.setPins(PIN_SD_CLK, PIN_SD_CMD, PIN_SD_D0);
#endif
    bool ok = SD_MMC.begin("/sdcard", true);
#endif
    if (!ok) {
        Serial.println("SD card mount FAILED");
        return false;
    }
    Serial.printf("SD card: %llu MB total, %llu MB used\n",
                  SDCARD.totalBytes() / (1024 * 1024), SDCARD.usedBytes() / (1024 * 1024));
    return true;
}
