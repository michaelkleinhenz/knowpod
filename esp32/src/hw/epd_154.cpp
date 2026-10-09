#ifdef BOARD_EPAPER_154

#include "epd.h"
#include <SPI.h>
#include "board.h"
#include "exio.h"
#include "debug.h"

// SSD1681 driver for the 1.54" V2 200x200 black/white panel. Command
// sequences and waveforms follow Waveshare's driver for this board
// (ESP32-C6-ePaper-1.54, port_bsp/port_display.cpp).
//
// The SD card shares the SPI bus, so every transfer holds the bus lock
// (beginTransaction) for as long as CS is low.

static const SPISettings spi_settings(20000000, MSBFIRST, SPI_MODE0);

// Full refresh (~1.5 s, flashes)
static const uint8_t WF_FULL[159] = {
    0x80, 0x48, 0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x40, 0x48, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x80, 0x48, 0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x40, 0x48, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0A,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x08, 0x01, 0x00, 0x08, 0x01, 0x00, 0x02,
    0x0A, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x22, 0x22, 0x22, 0x22, 0x22, 0x22, 0x00,
    0x00, 0x00, 0x22, 0x17, 0x41, 0x00, 0x32, 0x20,
};

// Partial refresh (~0.3 s, no flashing)
static const uint8_t WF_PARTIAL[159] = {
    0x00, 0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x80, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x40, 0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x00, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x0F, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x01, 0x00, 0x00, 0x00,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x22, 0x22, 0x22, 0x22, 0x22, 0x22, 0x00, 0x00, 0x00, 0x02,
    0x17, 0x41, 0xB0, 0x32, 0x28,
};

static void transfer(bool is_data, const uint8_t *buf, size_t len)
{
    SPI.beginTransaction(spi_settings);
    digitalWrite(PIN_EPD_DC, is_data ? HIGH : LOW);
    digitalWrite(PIN_EPD_CS, LOW);
    SPI.writeBytes(buf, len);
    digitalWrite(PIN_EPD_CS, HIGH);
    SPI.endTransaction();
}

static void send_command(uint8_t cmd)              { transfer(false, &cmd, 1); }
static void send_data(uint8_t data)                { transfer(true, &data, 1); }
static void send_buffer(const uint8_t *buf, size_t len) { transfer(true, buf, len); }

// SSD1681: BUSY HIGH = processing. A full refresh takes ~1.5 s. Once one wait
// times out the panel is not responding, so the rest of this refresh skips
// its waits instead of blocking the UI for many timeouts in a row.
#define BUSY_TIMEOUT_MS  5000

static bool stalled = false;     // a BUSY wait timed out during this refresh
static bool need_full = false;   // panel state unknown: next refresh starts from scratch

static void wait_busy()
{
    if (stalled) return;
    uint32_t start = millis();
    while (digitalRead(PIN_EPD_BUSY) == HIGH) {
        if (millis() - start > BUSY_TIMEOUT_MS) {
            Serial.println("EPD: BUSY timeout, panel not responding");
            stalled = true;
            return;
        }
        delay(2);
    }
}

// Also wakes the controller from deep sleep, which needs a longer pulse than
// the 3.97's SSD1677 (Waveshare's reference for this panel uses 20 ms).
static void reset()
{
    digitalWrite(PIN_EPD_RST, HIGH);
    delay(20);
    digitalWrite(PIN_EPD_RST, LOW);
    delay(20);
    digitalWrite(PIN_EPD_RST, HIGH);
    delay(20);
}

static void set_lut(const uint8_t *lut)
{
    send_command(0x32);
    send_buffer(lut, 153);
    wait_busy();

    send_command(0x3F);  // end option
    send_data(lut[153]);
    send_command(0x03);  // gate voltage
    send_data(lut[154]);
    send_command(0x04);  // source voltage
    send_data(lut[155]);
    send_data(lut[156]);
    send_data(lut[157]);
    send_command(0x2C);  // VCOM
    send_data(lut[158]);
}

static void init_full()
{
    reset();
    wait_busy();
    send_command(0x12);  // software reset
    wait_busy();

    send_command(0x01);  // driver output control: 200 gates
    send_data(0xC7);
    send_data(0x00);
    send_data(0x01);

    send_command(0x11);  // data entry mode: X increment, Y decrement
    send_data(0x01);

    send_command(0x44);  // RAM X start/end (in bytes)
    send_data(0x00);
    send_data((EPD_WIDTH - 1) >> 3);
    send_command(0x45);  // RAM Y start/end
    send_data((EPD_HEIGHT - 1) & 0xFF);
    send_data((EPD_HEIGHT - 1) >> 8);
    send_data(0x00);
    send_data(0x00);

    send_command(0x3C);  // border waveform
    send_data(0x01);

    send_command(0x18);  // internal temperature sensor
    send_data(0x80);

    send_command(0x22);  // load temperature and waveform setting
    send_data(0xB1);
    send_command(0x20);

    send_command(0x4E);  // RAM X counter
    send_data(0x00);
    send_command(0x4F);  // RAM Y counter
    send_data((EPD_HEIGHT - 1) & 0xFF);
    send_data((EPD_HEIGHT - 1) >> 8);
    wait_busy();

    set_lut(WF_FULL);
}

static void init_partial()
{
    // A hardware reset (no software reset) keeps the RAM, which holds the
    // image currently on the panel that the partial waveform diffs against.
    reset();
    wait_busy();

    set_lut(WF_PARTIAL);

    static const uint8_t opts[] = {0x00, 0x00, 0x00, 0x00, 0x00, 0x40, 0x00, 0x00, 0x00, 0x00};
    send_command(0x37);  // OTP options: RAM ping-pong for display mode 2
    send_buffer(opts, sizeof(opts));

    send_command(0x3C);  // border waveform
    send_data(0x80);

    send_command(0x22);  // enable clock and analog
    send_data(0xC0);
    send_command(0x20);
    wait_busy();
}

static void update(uint8_t sequence)
{
    send_command(0x22);
    send_data(sequence);
    send_command(0x20);
    wait_busy();
}

static void deep_sleep()
{
    send_command(0x10);  // deep sleep mode 1 (keeps RAM)
    send_data(0x01);
    delay(10);
}

void epd_begin()
{
    exio_set(EXIO_EPD_PWR, true);
    delay(10);

    pinMode(PIN_EPD_BUSY, INPUT);
    pinMode(PIN_EPD_RST, OUTPUT);
    pinMode(PIN_EPD_DC, OUTPUT);
    pinMode(PIN_EPD_CS, OUTPUT);
    digitalWrite(PIN_EPD_CS, HIGH);
    digitalWrite(PIN_EPD_RST, HIGH);
    // Keep the SD card deselected while we talk to the panel
    pinMode(PIN_SD_CS, OUTPUT);
    digitalWrite(PIN_SD_CS, HIGH);
    SPI.begin(PIN_SPI_SCK, PIN_SPI_MISO, PIN_SPI_MOSI, -1);  // no-op if the SD card started it
}

void epd_show(const uint8_t *fb, EpdRefresh mode)
{
    uint32_t start = millis();
    stalled = false;
    if (need_full) mode = EPD_REFRESH_FULL;  // a partial refresh needs a known base image

    if (mode == EPD_REFRESH_PARTIAL) {
        debug_stage = "epd: partial init";
        init_partial();
        debug_stage = "epd: write";
        send_command(0x24);
        send_buffer(fb, EPD_FB_SIZE);
        debug_stage = "epd: update";
        update(0xCF);
    } else {
        // Both RAMs get the image, so it is the base for the next partial refresh
        debug_stage = "epd: full init";
        init_full();
        debug_stage = "epd: write";
        send_command(0x24);
        send_buffer(fb, EPD_FB_SIZE);
        send_command(0x26);
        send_buffer(fb, EPD_FB_SIZE);
        debug_stage = "epd: update";
        update(0xC7);
    }
    debug_stage = "epd: sleep";
    deep_sleep();
    need_full = stalled;
    Serial.printf("EPD: %s refresh %s in %lu ms\n", mode == EPD_REFRESH_PARTIAL ? "partial" : "full",
                  stalled ? "FAILED" : "done", (unsigned long)(millis() - start));
}

#endif // BOARD_EPAPER_154
