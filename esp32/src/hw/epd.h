#pragma once

#include <Arduino.h>
#include "board.h"

#if defined(BOARD_EPAPER_397)
// 3.97" 800x480 black/white panel (SSD1677-compatible controller).
#define EPD_WIDTH   800
#define EPD_HEIGHT  480
#elif defined(BOARD_EPAPER_154)
// 1.54" 200x200 black/white panel (SSD1681 controller).
#define EPD_WIDTH   200
#define EPD_HEIGHT  200
#endif

// Framebuffer: 1 bit per pixel, MSB first, rows of EPD_WIDTH / 8 bytes, 1 = white.
#define EPD_FB_SIZE (EPD_WIDTH / 8 * EPD_HEIGHT)

enum EpdRefresh {
    EPD_REFRESH_FULL,     // standard waveform, removes all ghosting
    EPD_REFRESH_FAST,     // faster waveform (same as full on the 1.54")
    EPD_REFRESH_PARTIAL,  // no flashing
};

void epd_begin();

// Shows `fb` on the panel and puts the controller into deep sleep afterwards.
void epd_show(const uint8_t *fb, EpdRefresh mode);

// Before the chip's deep sleep: switches the panel's supply off where the board
// can (1.54"). The image stays; the next boot starts with a full refresh.
void epd_power_off();
