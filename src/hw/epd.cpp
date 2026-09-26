#include "epd.h"
#include <SPI.h>
#include "board.h"

// Command sequences follow Waveshare's factory firmware
// (ESP-IDF/08_ESP32-S3_e-Paper-3.97/components/epaper_port).

static SPIClass epd_spi(FSPI);
static const SPISettings spi_settings(20000000, MSBFIRST, SPI_MODE0);

static void send_command(uint8_t cmd)
{
    digitalWrite(PIN_EPD_DC, LOW);
    digitalWrite(PIN_EPD_CS, LOW);
    epd_spi.beginTransaction(spi_settings);
    epd_spi.write(cmd);
    epd_spi.endTransaction();
    digitalWrite(PIN_EPD_CS, HIGH);
}

static void send_data(uint8_t data)
{
    digitalWrite(PIN_EPD_DC, HIGH);
    digitalWrite(PIN_EPD_CS, LOW);
    epd_spi.beginTransaction(spi_settings);
    epd_spi.write(data);
    epd_spi.endTransaction();
    digitalWrite(PIN_EPD_CS, HIGH);
}

static void send_buffer(const uint8_t *buf, size_t len)
{
    digitalWrite(PIN_EPD_DC, HIGH);
    digitalWrite(PIN_EPD_CS, LOW);
    epd_spi.beginTransaction(spi_settings);
    epd_spi.writeBytes(buf, len);
    epd_spi.endTransaction();
    digitalWrite(PIN_EPD_CS, HIGH);
}

static void wait_busy()
{
    delay(20);
    while (digitalRead(PIN_EPD_BUSY) == HIGH)
        delay(5);
}

static void reset()
{
    digitalWrite(PIN_EPD_RST, HIGH);
    delay(20);
    digitalWrite(PIN_EPD_RST, LOW);
    delay(2);
    digitalWrite(PIN_EPD_RST, HIGH);
    delay(20);
}

static void set_full_window()
{
    send_command(0x44);  // RAM X start/end
    send_data(0x00);
    send_data(0x00);
    send_data((EPD_WIDTH - 1) & 0xFF);
    send_data((EPD_WIDTH - 1) >> 8);

    send_command(0x45);  // RAM Y start/end
    send_data((EPD_HEIGHT - 1) & 0xFF);
    send_data((EPD_HEIGHT - 1) >> 8);
    send_data(0x00);
    send_data(0x00);

    send_command(0x4E);  // RAM X counter
    send_data(0x00);
    send_data(0x00);
    send_command(0x4F);  // RAM Y counter
    send_data(0x00);
    send_data(0x00);
}

static void init_panel(bool fast)
{
    reset();
    wait_busy();
    send_command(0x12);  // software reset
    wait_busy();

    send_command(0x18);  // internal temperature sensor
    send_data(0x80);

    send_command(0x0C);  // booster soft start
    send_data(0xAE);
    send_data(0xC7);
    send_data(0xC3);
    send_data(0xC0);
    send_data(0x80);

    send_command(0x01);  // driver output control
    send_data((EPD_HEIGHT - 1) & 0xFF);
    send_data((EPD_HEIGHT - 1) >> 8);
    send_data(0x02);

    send_command(0x3C);  // border waveform
    send_data(0x01);

    send_command(0x11);  // data entry mode
    send_data(0x01);

    set_full_window();
    wait_busy();

    if (fast) {
        send_command(0x1A);  // temperature override selects the fast LUT
        send_data(0x6A);
    }
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
    send_command(0x10);
    send_data(0x01);
    delay(10);
}

void epd_begin()
{
    pinMode(PIN_EPD_BUSY, INPUT);
    pinMode(PIN_EPD_RST, OUTPUT);
    pinMode(PIN_EPD_DC, OUTPUT);
    pinMode(PIN_EPD_CS, OUTPUT);
    digitalWrite(PIN_EPD_CS, HIGH);
    digitalWrite(PIN_EPD_RST, HIGH);
    epd_spi.begin(PIN_EPD_SCK, -1, PIN_EPD_MOSI, -1);
}

void epd_show(const uint8_t *fb, EpdRefresh mode)
{
    // A partial refresh drives only the pixels that differ between RAM 0x24
    // (new image) and RAM 0x26 (image currently on the panel). The controller
    // does not update 0x26 by itself, so we keep a copy of what is on screen
    // and write it as the "old" image every time.
    static uint8_t *on_screen = nullptr;
    if (!on_screen) on_screen = (uint8_t *)ps_malloc(EPD_FB_SIZE);
    if (!on_screen) mode = EPD_REFRESH_FAST;

    if (mode == EPD_REFRESH_PARTIAL) {
        init_panel(false);
        send_command(0x3C);  // border waveform for partial updates
        send_data(0x80);
        send_command(0x26);
        send_buffer(on_screen, EPD_FB_SIZE);
        send_command(0x24);
        send_buffer(fb, EPD_FB_SIZE);
        update(0xFF);
    } else {
        bool fast = mode == EPD_REFRESH_FAST;
        init_panel(fast);
        send_command(0x24);
        send_buffer(fb, EPD_FB_SIZE);
        send_command(0x26);
        send_buffer(fb, EPD_FB_SIZE);
        update(fast ? 0xD7 : 0xF7);
    }
    if (on_screen) memcpy(on_screen, fb, EPD_FB_SIZE);
    deep_sleep();
}
