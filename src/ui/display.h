#pragma once

#include <Arduino.h>
#include <Adafruit_GFX.h>
#include <vector>

// Portrait drawing surface on top of the e-paper driver (same orientation as
// the Waveshare factory firmware). Text is UTF-8; the fonts cover Latin-1 and
// Latin Extended-A, i.e. Western European languages.

#define SCREEN_W  480
#define SCREEN_H  800

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

// Draws with y at the text baseline. Returns the x after the text.
int draw_text(int x, int y, const char *text, Font font, uint16_t color = INK);
void draw_text_centered(int y, const char *text, Font font, uint16_t color = INK);
int text_width(const char *text, Font font);
int font_ascent(Font font);
int line_height(Font font);

// knowpod logo: a round badge with a sound wave, followed by the wordmark,
// centered horizontally on `cx` and vertically on `cy`. `mark` is the badge
// diameter in pixels.
void draw_logo(int cx, int cy, int mark = 56);

// Word-wraps UTF-8 text to `width` pixels; '\n' starts a new paragraph.
std::vector<String> wrap_text(const String &text, Font font, int width);

// Sends the canvas to the panel. Uses a partial refresh, with a fast full
// refresh every few updates (if `auto_clean`) or when `clean` is set to
// clear ghosting.
void display_update(bool clean = false, bool auto_clean = true);
