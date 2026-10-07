#ifdef BOARD_EPAPER_154G

#include "epd.h"
#include "board.h"

// UC8253 driver for the 200x200 4-color (B/W/R/Y) panel on the 1.54G board.
// The UI draws into a 1-bit (B/W) GFXcanvas1; this driver expands each pixel
// to 2 bits (00 = black, 01 = white) when transmitting to the controller.
//
// Software SPI: the Waveshare reference code bit-bangs rather than using
// hardware SPI, and the UC8253 is not timing-critical, so we do the same.

static void spi_write_byte(uint8_t data)
{
    for (int i = 0; i < 8; i++) {
        digitalWrite(PIN_EPD_MOSI, (data & 0x80) ? HIGH : LOW);
        data <<= 1;
        digitalWrite(PIN_EPD_SCK, HIGH);
        digitalWrite(PIN_EPD_SCK, LOW);
    }
}

static void send_command(uint8_t cmd)
{
    digitalWrite(PIN_EPD_DC, LOW);
    digitalWrite(PIN_EPD_CS, LOW);
    spi_write_byte(cmd);
    digitalWrite(PIN_EPD_CS, HIGH);
}

static void send_data(uint8_t data)
{
    digitalWrite(PIN_EPD_DC, HIGH);
    digitalWrite(PIN_EPD_CS, LOW);
    spi_write_byte(data);
    digitalWrite(PIN_EPD_CS, HIGH);
}

static void send_buffer(const uint8_t *buf, size_t len)
{
    digitalWrite(PIN_EPD_DC, HIGH);
    digitalWrite(PIN_EPD_CS, LOW);
    for (size_t i = 0; i < len; i++)
        spi_write_byte(buf[i]);
    digitalWrite(PIN_EPD_CS, HIGH);
}

// UC8253: BUSY LOW = processing, HIGH = ready
static void wait_busy()
{
    delay(100);
    uint32_t start = millis();
    while (digitalRead(PIN_EPD_BUSY) == LOW) {
        if (millis() - start > 30000) {
            Serial.println("EPD: BUSY timeout!");
            return;
        }
        delay(5);
    }
}

static void reset()
{
    digitalWrite(PIN_EPD_RST, HIGH);
    delay(200);
    digitalWrite(PIN_EPD_RST, LOW);
    delay(2);
    digitalWrite(PIN_EPD_RST, HIGH);
    delay(200);
}

static void init_panel(bool fast)
{
    reset();

    send_command(0x4D);
    send_data(0x78);

    send_command(0x00);  // PSR
    send_data(0x0F);
    send_data(0x29);

    send_command(0x06);  // BTST (booster)
    send_data(0x0D);
    send_data(0x12);
    send_data(0x30);
    send_data(0x20);
    send_data(0x19);
    send_data(0x2A);
    send_data(0x22);

    send_command(0x50);  // CDI
    send_data(0x37);

    send_command(0x61);  // TRES (resolution)
    send_data(0x00);
    send_data(EPD_WIDTH);
    send_data(0x00);
    send_data(EPD_HEIGHT);

    send_command(0xE9);
    send_data(0x01);

    send_command(0x30);  // PLL
    send_data(0x08);

    send_command(0x04);  // power on
    wait_busy();

    if (fast) {
        send_command(0xE0);
        send_data(0x02);
        send_command(0xE6);
        send_data(0x5D);
        send_command(0xA5);
        send_data(0x00);
        wait_busy();
    }
}

static void refresh()
{
    send_command(0x12);  // DRF
    send_data(0x00);
    wait_busy();
}

static void deep_sleep()
{
    send_command(0x02);  // power off
    send_data(0x00);
    wait_busy();
    send_command(0x07);  // deep sleep
    send_data(0xA5);
    delay(10);
}

// Maps a 1-bpp nibble (4 pixels) to 2-bpp positions 6,4,2,0 of a byte.
// bw bit 1 (white) → 01, bw bit 0 (black) → 00 at each 2-bit position.
static const uint8_t spread[16] = {
    0x00, 0x01, 0x04, 0x05, 0x10, 0x11, 0x14, 0x15,
    0x40, 0x41, 0x44, 0x45, 0x50, 0x51, 0x54, 0x55,
};

static const uint8_t *color_plane = nullptr;

void epd_set_color_plane(const uint8_t *color_fb)
{
    color_plane = color_fb;
}

// Sends the combined B/W + color framebuffers as 2-bpp data (command 0x10).
// Per pixel: {color_bit, bw_bit} → 00=black, 01=white, 10=yellow, 11=red.
static void send_fb_as_2bpp(const uint8_t *fb)
{
    uint8_t row[EPD_WIDTH / 4];
    int src_stride = EPD_WIDTH / 8;

    send_command(0x10);
    for (int y = 0; y < EPD_HEIGHT; y++) {
        const uint8_t *bw = fb + y * src_stride;
        const uint8_t *col = color_plane ? color_plane + y * src_stride : nullptr;
        for (int i = 0; i < src_stride; i++) {
            uint8_t b = bw[i];
            uint8_t c = col ? col[i] : 0;
            // spread[] places each source bit at even positions (6,4,2,0);
            // shifting the color spread left by 1 places it at odd positions
            // (7,5,3,1), forming {col, bw} pairs per pixel.
            row[i * 2]     = (spread[c >> 4] << 1) | spread[b >> 4];
            row[i * 2 + 1] = (spread[c & 0xF] << 1) | spread[b & 0xF];
        }
        send_buffer(row, sizeof(row));
    }
}

void epd_begin()
{
    Serial.println("EPD: begin (1.54G)");
    pinMode(PIN_EPD_PWR, OUTPUT);
    digitalWrite(PIN_EPD_PWR, HIGH);
    delay(50);

    pinMode(PIN_EPD_SCK, OUTPUT);
    pinMode(PIN_EPD_MOSI, OUTPUT);
    pinMode(PIN_EPD_BUSY, INPUT);
    pinMode(PIN_EPD_RST, OUTPUT);
    pinMode(PIN_EPD_DC, OUTPUT);
    pinMode(PIN_EPD_CS, OUTPUT);
    digitalWrite(PIN_EPD_CS, HIGH);
    digitalWrite(PIN_EPD_SCK, LOW);
    digitalWrite(PIN_EPD_RST, HIGH);
    Serial.printf("EPD: PWR=%d BUSY=%d RST=%d\n",
                  digitalRead(PIN_EPD_PWR), digitalRead(PIN_EPD_BUSY), digitalRead(PIN_EPD_RST));
}

void epd_show(const uint8_t *fb, EpdRefresh mode)
{
    Serial.printf("EPD: show (mode=%d)\n", mode);
    bool fast = mode != EPD_REFRESH_FULL;
    init_panel(fast);
    Serial.println("EPD: panel init done, sending framebuffer");
    send_fb_as_2bpp(fb);
    Serial.println("EPD: refreshing");
    refresh();
    Serial.println("EPD: done, entering sleep");
    deep_sleep();
}

#endif // BOARD_EPAPER_154G
