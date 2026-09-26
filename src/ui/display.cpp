#include "display.h"
#include <U8g2_for_Adafruit_GFX.h>
#include "hw/epd.h"

#define PARTIALS_BEFORE_CLEAN  12
#define LINE_SPACING           6

static GFXcanvas1 *canvas;
static U8G2_FOR_ADAFRUIT_GFX u8g2;
static bool has_base = false;
static int partials = 0;

static const uint8_t *font_data(Font font)
{
    switch (font) {
    case FONT_SMALL:  return u8g2_font_luBS14_te;
    case FONT_BODY:   return u8g2_font_luRS18_te;
    case FONT_BOLD:   return u8g2_font_luBS18_te;
    case FONT_TITLE:  return u8g2_font_luBS24_te;
    case FONT_DIGITS: return u8g2_font_fub42_tn;
    }
    return u8g2_font_luRS18_te;
}

void display_begin()
{
    if (canvas) return;
    epd_begin();
    canvas = new GFXcanvas1(EPD_WIDTH, EPD_HEIGHT);
    canvas->setRotation(3);  // portrait, as in the factory firmware
    u8g2.begin(*canvas);
    u8g2.setFontMode(1);     // transparent background
    display_clear();
}

GFXcanvas1 &gfx()
{
    return *canvas;
}

void display_clear()
{
    canvas->fillScreen(PAPER);
}

int draw_text(int x, int y, const char *text, Font font, uint16_t color)
{
    u8g2.setFont(font_data(font));
    u8g2.setFontMode(1);  // transparent; setFont() silently turns this off
    u8g2.setForegroundColor(color);
    u8g2.setBackgroundColor(color == INK ? PAPER : INK);
    return x + u8g2.drawUTF8(x, y, text);
}

void draw_text_centered(int y, const char *text, Font font, uint16_t color)
{
    draw_text((SCREEN_W - text_width(text, font)) / 2, y, text, font, color);
}

int text_width(const char *text, Font font)
{
    u8g2.setFont(font_data(font));
    return u8g2.getUTF8Width(text);
}

int font_ascent(Font font)
{
    u8g2.setFont(font_data(font));
    return u8g2.getFontAscent();
}

int line_height(Font font)
{
    u8g2.setFont(font_data(font));
    return u8g2.getFontAscent() - u8g2.getFontDescent() + LINE_SPACING;
}

void draw_logo(int cx, int cy, int mark)
{
    GFXcanvas1 &g = *canvas;
    const char *word = "knowpod";
    int gap = mark / 4;
    int total = mark + gap + text_width(word, FONT_TITLE);
    int x = cx - total / 2;

    // Badge: black disc with a symmetric sound wave knocked out in white
    int r = mark / 2;
    g.fillCircle(x + r, cy, r, INK);
    static const float heights[] = {0.22f, 0.44f, 0.64f, 0.44f, 0.22f};
    int bar_w = max(3, mark / 11);
    int bar_gap = max(2, mark / 14);
    int bars_w = 5 * bar_w + 4 * bar_gap;
    int bx = x + r - bars_w / 2;
    for (float h : heights) {
        int bh = max(bar_w, (int)(h * mark));
        g.fillRoundRect(bx, cy - bh / 2, bar_w, bh, bar_w / 2, PAPER);
        bx += bar_w + bar_gap;
    }

    // Wordmark, vertically centered on the badge
    draw_text(x + mark + gap, cy + font_ascent(FONT_TITLE) / 2, word, FONT_TITLE);
}

// Appends `word` to `lines`, splitting it at UTF-8 character boundaries
// if it is wider than `width` on its own.
static void break_long_word(const String &word, Font font, int width, std::vector<String> &lines,
                            String &line)
{
    for (size_t i = 0; i < word.length();) {
        size_t len = 1;
        while (i + len < word.length() && (word[i + len] & 0xC0) == 0x80) len++;
        String ch = word.substring(i, i + len);
        if (!line.isEmpty() && text_width((line + ch).c_str(), font) > width) {
            lines.push_back(line);
            line = "";
        }
        line += ch;
        i += len;
    }
}

std::vector<String> wrap_text(const String &text, Font font, int width)
{
    std::vector<String> lines;
    int start = 0;
    while (start <= (int)text.length()) {
        int nl = text.indexOf('\n', start);
        String para = text.substring(start, nl < 0 ? text.length() : nl);
        start = nl < 0 ? text.length() + 1 : nl + 1;

        String line;
        int pos = 0;
        while (pos < (int)para.length()) {
            int sp = para.indexOf(' ', pos);
            String word = para.substring(pos, sp < 0 ? para.length() : sp);
            pos = sp < 0 ? para.length() : sp + 1;
            if (word.isEmpty()) continue;

            String candidate = line.isEmpty() ? word : line + " " + word;
            if (text_width(candidate.c_str(), font) <= width) {
                line = candidate;
                continue;
            }
            if (!line.isEmpty()) lines.push_back(line);
            line = "";
            if (text_width(word.c_str(), font) <= width)
                line = word;
            else
                break_long_word(word, font, width, lines, line);
        }
        lines.push_back(line);
    }
    return lines;
}

void display_update(bool clean, bool auto_clean)
{
    const uint8_t *fb = canvas->getBuffer();
    if (!has_base) {
        epd_show(fb, EPD_REFRESH_FULL);
        has_base = true;
        partials = 0;
    } else if (clean || (auto_clean && partials >= PARTIALS_BEFORE_CLEAN)) {
        epd_show(fb, EPD_REFRESH_FAST);
        partials = 0;
    } else {
        epd_show(fb, EPD_REFRESH_PARTIAL);
        partials++;
    }
}
