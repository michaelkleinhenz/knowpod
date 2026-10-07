#pragma once

#include <Arduino.h>
#include "board.h"

#if defined(BOARD_EPAPER_397)
// 3.97" 800x480 black/white panel (SSD1677-compatible controller).
// Framebuffer: 1 bit per pixel, MSB first, rows of 100 bytes, 1 = white.
#define EPD_WIDTH   800
#define EPD_HEIGHT  480
#elif defined(BOARD_EPAPER_154G)
// 1.54" 200x200 4-color panel (UC8253 controller).
// The UI draws a 1-bpp B/W framebuffer; the driver expands to 2 bpp on send.
#define EPD_WIDTH   200
#define EPD_HEIGHT  200
#endif

#define EPD_FB_SIZE (EPD_WIDTH / 8 * EPD_HEIGHT)

enum EpdRefresh {
    EPD_REFRESH_FULL,     // standard waveform, removes all ghosting
    EPD_REFRESH_FAST,     // faster waveform
    EPD_REFRESH_PARTIAL,  // no flashing (3.97 only; falls back to fast on 1.54G)
};

void epd_begin();

// Shows `fb` on the panel and puts the controller into deep sleep afterwards.
void epd_show(const uint8_t *fb, EpdRefresh mode);

#ifdef BOARD_EPAPER_154G
// Sets a second 1-bpp plane that selects color. Each pixel is composed from
// {bw_bit, color_bit} → 00=black, 01=white, 10=yellow, 11=red.
void epd_set_color_plane(const uint8_t *color_fb);
#endif
