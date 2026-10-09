#pragma once

#include "board.h"

// The SD card's file system: SD_MMC on the 3.97" board, SD over the SPI bus
// shared with the e-paper on the 1.54" board (the ESP32-C6 has no SDMMC host).
#ifdef SD_SPI
#include <SD.h>
#define SDCARD SD
#else
#include <SD_MMC.h>
#define SDCARD SD_MMC
#endif

// Mounts the card at /sdcard.
bool sdcard_begin();
