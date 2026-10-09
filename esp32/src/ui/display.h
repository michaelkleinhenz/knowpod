#pragma once

#include <Arduino.h>
#include <Adafruit_GFX.h>
#include <vector>
#include "board.h"

// Portrait drawing surface on top of the e-paper driver. Text is UTF-8;
// the fonts cover Latin-1 and Latin Extended-A.

#if defined(BOARD_EPAPER_397)
#define SCREEN_W  480
#define SCREEN_H  800
#elif defined(BOARD_EPAPER_154)
#define SCREEN_W  200
#define SCREEN_H  200
#endif

#define INK    0   // black
#define PAPER  1   // white

enum Font : uint8_t {
    FONT_SMALL,   // status bar, hints
    FONT_BODY,    // reading text
    FONT_BOLD,
    FONT_TITLE,
    FONT_DIGITS,  // big numbers (0-9 : . only)
};

void display_begin();
GFXcanvas1 &gfx();

void display_clear();

int draw_text(int x, int y, const char *text, Font font, uint16_t color = INK);
void draw_text_centered(int y, const char *text, Font font, uint16_t color = INK);
int text_width(const char *text, Font font);
int font_ascent(Font font);
int line_height(Font font);

void draw_logo(int cx, int cy, int mark = 56);

std::vector<String> wrap_text(const String &text, Font font, int width);

void display_update(bool clean = false, bool auto_clean = true);

