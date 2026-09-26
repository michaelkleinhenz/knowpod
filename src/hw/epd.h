#pragma once

#include <Arduino.h>

// Driver for the 3.97" 800x480 black/white e-paper panel.
// Framebuffer: 1 bit per pixel, MSB first, rows of 100 bytes, 1 = white.

#define EPD_WIDTH   800
#define EPD_HEIGHT  480
#define EPD_FB_SIZE (EPD_WIDTH / 8 * EPD_HEIGHT)

enum EpdRefresh {
    EPD_REFRESH_FULL,     // standard waveform, flashes, removes all ghosting (~3 s)
    EPD_REFRESH_FAST,     // fast full waveform, flashes briefly (~1.5 s)
    EPD_REFRESH_PARTIAL,  // no flashing, accumulates ghosting (~0.4 s)
};

void epd_begin();

// Shows `fb` on the panel and puts the controller into deep sleep afterwards.
// A partial refresh needs a previous full/fast refresh as its base.
void epd_show(const uint8_t *fb, EpdRefresh mode);
