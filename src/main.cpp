#include <Arduino.h>
#include "EPD_3in97.h"

#define WIDTH  EPD_3IN97_WIDTH
#define HEIGHT EPD_3IN97_HEIGHT
#define FB_STRIDE ((WIDTH + 7) / 8)
#define FB_SIZE   (FB_STRIDE * HEIGHT)

static uint8_t *fb;

static void fb_clear(uint8_t color)
{
    memset(fb, color ? 0xFF : 0x00, FB_SIZE);
}

static void fb_set_pixel(int x, int y, bool white)
{
    if (x < 0 || x >= WIDTH || y < 0 || y >= HEIGHT) return;
    uint32_t byte_idx = y * FB_STRIDE + x / 8;
    uint8_t bit_mask = 0x80 >> (x % 8);
    if (white)
        fb[byte_idx] |= bit_mask;
    else
        fb[byte_idx] &= ~bit_mask;
}

static void fb_fill_rect(int x0, int y0, int w, int h, bool white)
{
    for (int y = y0; y < y0 + h && y < HEIGHT; y++)
        for (int x = x0; x < x0 + w && x < WIDTH; x++)
            fb_set_pixel(x, y, white);
}

static void fb_draw_rect(int x0, int y0, int w, int h, int thickness, bool white)
{
    fb_fill_rect(x0, y0, w, thickness, white);
    fb_fill_rect(x0, y0 + h - thickness, w, thickness, white);
    fb_fill_rect(x0, y0, thickness, h, white);
    fb_fill_rect(x0 + w - thickness, y0, thickness, h, white);
}

static void fb_draw_circle(int cx, int cy, int r, bool white)
{
    int x = 0, y = r, d = 3 - 2 * r;
    while (x <= y) {
        fb_set_pixel(cx + x, cy + y, white);
        fb_set_pixel(cx - x, cy + y, white);
        fb_set_pixel(cx + x, cy - y, white);
        fb_set_pixel(cx - x, cy - y, white);
        fb_set_pixel(cx + y, cy + x, white);
        fb_set_pixel(cx - y, cy + x, white);
        fb_set_pixel(cx + y, cy - x, white);
        fb_set_pixel(cx - y, cy - x, white);
        if (d < 0) d += 4 * x + 6;
        else { d += 4 * (x - y) + 10; y--; }
        x++;
    }
}

static void fb_fill_circle(int cx, int cy, int r, bool white)
{
    for (int y = -r; y <= r; y++)
        for (int x = -r; x <= r; x++)
            if (x * x + y * y <= r * r)
                fb_set_pixel(cx + x, cy + y, white);
}

static void fb_draw_line(int x0, int y0, int x1, int y1, bool white)
{
    int dx = abs(x1 - x0), sx = x0 < x1 ? 1 : -1;
    int dy = -abs(y1 - y0), sy = y0 < y1 ? 1 : -1;
    int err = dx + dy;
    for (;;) {
        fb_set_pixel(x0, y0, white);
        if (x0 == x1 && y0 == y1) break;
        int e2 = 2 * err;
        if (e2 >= dy) { err += dy; x0 += sx; }
        if (e2 <= dx) { err += dx; y0 += sy; }
    }
}

// Minimal 5x7 font for uppercase + digits + space
static const uint8_t FONT_5X7[][5] = {
    {0x00,0x00,0x00,0x00,0x00}, // ' '
    {0x7E,0x11,0x11,0x11,0x7E}, // 'A'
    {0x7F,0x49,0x49,0x49,0x36}, // 'B'
    {0x3E,0x41,0x41,0x41,0x22}, // 'C'
    {0x7F,0x41,0x41,0x22,0x1C}, // 'D'
    {0x7F,0x49,0x49,0x49,0x41}, // 'E'
    {0x7F,0x09,0x09,0x09,0x01}, // 'F'
    {0x3E,0x41,0x49,0x49,0x7A}, // 'G'
    {0x7F,0x08,0x08,0x08,0x7F}, // 'H'
    {0x00,0x41,0x7F,0x41,0x00}, // 'I'
    {0x20,0x40,0x41,0x3F,0x01}, // 'J'
    {0x7F,0x08,0x14,0x22,0x41}, // 'K'
    {0x7F,0x40,0x40,0x40,0x40}, // 'L'
    {0x7F,0x02,0x0C,0x02,0x7F}, // 'M'
    {0x7F,0x04,0x08,0x10,0x7F}, // 'N'
    {0x3E,0x41,0x41,0x41,0x3E}, // 'O'
    {0x7F,0x09,0x09,0x09,0x06}, // 'P'
    {0x3E,0x41,0x51,0x21,0x5E}, // 'Q'
    {0x7F,0x09,0x19,0x29,0x46}, // 'R'
    {0x46,0x49,0x49,0x49,0x31}, // 'S'
    {0x01,0x01,0x7F,0x01,0x01}, // 'T'
    {0x3F,0x40,0x40,0x40,0x3F}, // 'U'
    {0x1F,0x20,0x40,0x20,0x1F}, // 'V'
    {0x3F,0x40,0x38,0x40,0x3F}, // 'W'
    {0x63,0x14,0x08,0x14,0x63}, // 'X'
    {0x07,0x08,0x70,0x08,0x07}, // 'Y'
    {0x61,0x51,0x49,0x45,0x43}, // 'Z'
    {0x3E,0x51,0x49,0x45,0x3E}, // '0'
    {0x00,0x42,0x7F,0x40,0x00}, // '1'
    {0x42,0x61,0x51,0x49,0x46}, // '2'
    {0x21,0x41,0x45,0x4B,0x31}, // '3'
    {0x18,0x14,0x12,0x7F,0x10}, // '4'
    {0x27,0x45,0x45,0x45,0x39}, // '5'
    {0x3C,0x4A,0x49,0x49,0x30}, // '6'
    {0x01,0x71,0x09,0x05,0x03}, // '7'
    {0x36,0x49,0x49,0x49,0x36}, // '8'
    {0x06,0x49,0x49,0x29,0x1E}, // '9'
    {0x14,0x14,0x14,0x14,0x14}, // '-' (mapped to index 37)
    {0x36,0x36,0x00,0x00,0x00}, // ':' (mapped to index 38)
    {0x00,0x00,0x00,0x00,0x00}, // '.' (mapped to index 39)
};

static int font_index(char c)
{
    if (c == ' ') return 0;
    if (c >= 'A' && c <= 'Z') return c - 'A' + 1;
    if (c >= 'a' && c <= 'z') return c - 'a' + 1;
    if (c >= '0' && c <= '9') return c - '0' + 27;
    if (c == '-') return 37;
    if (c == ':') return 38;
    if (c == '.') return 39;
    return 0;
}

static void fb_draw_char(int x, int y, char c, int scale, bool white)
{
    int idx = font_index(c);
    for (int col = 0; col < 5; col++) {
        uint8_t line = FONT_5X7[idx][col];
        for (int row = 0; row < 7; row++) {
            if (line & (1 << row)) {
                for (int sy = 0; sy < scale; sy++)
                    for (int sx = 0; sx < scale; sx++)
                        fb_set_pixel(x + col * scale + sx,
                                     y + row * scale + sy, white);
            }
        }
    }
}

static void fb_draw_text(int x, int y, const char *str, int scale, bool white)
{
    while (*str) {
        fb_draw_char(x, y, *str, scale, white);
        x += 6 * scale;
        str++;
    }
}

void setup()
{
    Serial.begin(115200);
    delay(2000);
    Serial.println("\n=== ESP32-S3-ePaper-3.97 Display Demo ===");

    fb = (uint8_t *)ps_malloc(FB_SIZE);
    if (!fb) {
        Serial.println("PSRAM alloc FAILED");
        return;
    }
    Serial.printf("Framebuffer: %u bytes in PSRAM\n", FB_SIZE);

    DEV_Module_Init();

    // --- Screen 1: Clear to white ---
    Serial.println("Init + clear to white...");
    EPD_3IN97_Init();
    EPD_3IN97_Clear();
    delay(2000);

    // --- Screen 2: Draw a demo pattern ---
    Serial.println("Drawing demo pattern...");
    EPD_3IN97_Init_Fast();

    fb_clear(true);

    // Border
    fb_draw_rect(0, 0, WIDTH, HEIGHT, 3, false);

    // Title
    fb_draw_text(30, 20, "ESP32-S3 EPAPER 3.97", 4, false);
    fb_draw_text(30, 60, "800 X 480   WAVESHARE", 2, false);

    // Horizontal divider
    fb_fill_rect(20, 90, WIDTH - 40, 2, false);

    // Shapes demo
    fb_fill_rect(40, 110, 120, 80, false);
    fb_draw_rect(200, 110, 120, 80, 2, false);
    fb_fill_circle(420, 150, 40, false);
    fb_draw_circle(540, 150, 40, false);

    // Diagonal lines
    fb_draw_line(620, 110, 760, 190, false);
    fb_draw_line(620, 190, 760, 110, false);
    fb_draw_rect(620, 110, 140, 80, 1, false);

    // Checkerboard pattern
    int check_x = 40, check_y = 220, check_size = 20;
    for (int row = 0; row < 8; row++)
        for (int col = 0; col < 8; col++)
            if ((row + col) % 2 == 0)
                fb_fill_rect(check_x + col * check_size,
                             check_y + row * check_size,
                             check_size, check_size, false);
    fb_draw_rect(check_x, check_y, 8 * check_size, 8 * check_size, 1, false);

    // Concentric circles
    int cc_x = 360, cc_y = 340;
    for (int r = 20; r <= 100; r += 20)
        fb_draw_circle(cc_x, cc_y, r, false);

    // Labels
    fb_draw_text(40, 200, "FILLED", 1, false);
    fb_draw_text(200, 200, "OUTLINE", 1, false);
    fb_draw_text(520, 200, "LINES", 1, false);
    fb_draw_text(50, 410, "CHECKERBOARD", 2, false);
    fb_draw_text(500, 420, "HELLO WORLD", 3, false);

    // Gradient bars at the bottom
    for (int i = 0; i < 8; i++) {
        int bx = 500 + i * 30, by = 220;
        for (int y = by; y < by + 160; y++)
            for (int x = bx; x < bx + 25; x++)
                if ((x + y) % (i + 2) == 0)
                    fb_set_pixel(x, y, false);
        fb_draw_rect(bx, by, 25, 160, 1, false);
    }

    EPD_3IN97_Display_Fast(fb);
    Serial.println("Demo pattern displayed!");

    delay(5000);

    // --- Put display to sleep to preserve it ---
    Serial.println("Entering sleep...");
    EPD_3IN97_Init();
    EPD_3IN97_Clear();
    EPD_3IN97_Sleep();
    DEV_Module_Exit();

    free(fb);
    fb = NULL;
    Serial.println("Done. Display is sleeping.");
}

void loop()
{
}
