#include <Arduino.h>
#include "ESP_I2S.h"
#include "Wire.h"
#include "SD_MMC.h"
#include "FS.h"
#include "es8311.h"
#include "EPD_3in97.h"

// --- Pin definitions (Waveshare ESP32-S3-ePaper-3.97) ---

// I2C for ES8311 codec
#define I2C_SDA       41
#define I2C_SCL       42

// I2S audio
#define I2S_MCK_PIN   13
#define I2S_BCK_PIN   14
#define I2S_LRCK_PIN  47
#define I2S_DOUT_PIN  48
#define I2S_DIN_PIN   21
#define PA_CTRL       39

// SD card (4-bit SDMMC)
#define SD_CLK  16
#define SD_CMD  17
#define SD_D0   15
#define SD_D1   7
#define SD_D2   8
#define SD_D3   18

// Audio parameters
#define SAMPLE_RATE       24000
#define MCLK_MULTIPLE     256
#define MCLK_FREQ_HZ     (SAMPLE_RATE * MCLK_MULTIPLE)
#define VOICE_VOLUME      70
#define BITS_PER_SAMPLE   16
#define NUM_CHANNELS      1
#define AUDIO_BUF_SIZE    1024

I2SClass i2s;
static bool sd_ok = false;
static bool codec_ok = false;

// ============================================================
// WAV file helpers
// ============================================================

struct WavHeader {
    char     riff[4]       = {'R','I','F','F'};
    uint32_t file_size     = 0;
    char     wave[4]       = {'W','A','V','E'};
    char     fmt_id[4]     = {'f','m','t',' '};
    uint32_t fmt_size      = 16;
    uint16_t audio_format  = 1; // PCM
    uint16_t num_channels  = NUM_CHANNELS;
    uint32_t sample_rate   = SAMPLE_RATE;
    uint32_t byte_rate     = SAMPLE_RATE * NUM_CHANNELS * (BITS_PER_SAMPLE / 8);
    uint16_t block_align   = NUM_CHANNELS * (BITS_PER_SAMPLE / 8);
    uint16_t bits_per_samp = BITS_PER_SAMPLE;
    char     data_id[4]    = {'d','a','t','a'};
    uint32_t data_size     = 0;
};

static void wav_header_write(File &f, uint32_t data_bytes)
{
    WavHeader hdr;
    hdr.data_size = data_bytes;
    hdr.file_size = data_bytes + sizeof(WavHeader) - 8;
    f.seek(0);
    f.write((const uint8_t *)&hdr, sizeof(hdr));
}

// ============================================================
// Hardware init
// ============================================================

static bool init_sd_card()
{
    SD_MMC.setPins(SD_CLK, SD_CMD, SD_D0, SD_D1, SD_D2, SD_D3);
    if (!SD_MMC.begin("/sdcard", true)) {
        Serial.println("SD card mount FAILED");
        return false;
    }
    uint64_t total = SD_MMC.totalBytes() / (1024 * 1024);
    uint64_t used  = SD_MMC.usedBytes()  / (1024 * 1024);
    Serial.printf("SD card: %llu MB total, %llu MB used\n", total, used);
    return true;
}

static bool init_codec()
{
    Wire.begin(I2C_SDA, I2C_SCL);

    es8311_handle_t es = es8311_create(I2C_NUM_0, ES8311_ADDRRES_0);
    if (!es) {
        Serial.println("ES8311 create FAILED");
        return false;
    }

    const es8311_clock_config_t clk = {
        .mclk_inverted    = false,
        .sclk_inverted    = false,
        .mclk_from_mclk_pin = true,
        .mclk_frequency   = MCLK_FREQ_HZ,
        .sample_frequency = SAMPLE_RATE
    };

    if (es8311_init(es, &clk, ES8311_RESOLUTION_16, ES8311_RESOLUTION_16) != ESP_OK) {
        Serial.println("ES8311 init FAILED");
        return false;
    }
    es8311_voice_volume_set(es, VOICE_VOLUME, NULL);
    es8311_microphone_config(es, false);
    es8311_microphone_gain_set(es, ES8311_MIC_GAIN_42DB);

    Serial.println("ES8311 codec initialized");
    return true;
}

static bool init_i2s_playback()
{
    i2s.end();
    i2s.setPins(I2S_BCK_PIN, I2S_LRCK_PIN, I2S_DOUT_PIN, I2S_DIN_PIN, I2S_MCK_PIN);
    if (!i2s.begin(I2S_MODE_STD, SAMPLE_RATE, I2S_DATA_BIT_WIDTH_16BIT,
                   I2S_SLOT_MODE_MONO, I2S_STD_SLOT_LEFT)) {
        Serial.println("I2S playback init FAILED");
        return false;
    }
    return true;
}

static bool init_i2s_recording()
{
    i2s.end();
    i2s.setPins(I2S_BCK_PIN, I2S_LRCK_PIN, I2S_DOUT_PIN, I2S_DIN_PIN, I2S_MCK_PIN);
    if (!i2s.begin(I2S_MODE_STD, SAMPLE_RATE, I2S_DATA_BIT_WIDTH_16BIT,
                   I2S_SLOT_MODE_MONO, I2S_STD_SLOT_LEFT)) {
        Serial.println("I2S recording init FAILED");
        return false;
    }
    return true;
}

static void speaker_on()  { digitalWrite(PA_CTRL, HIGH); }
static void speaker_off() { digitalWrite(PA_CTRL, LOW);  }

// ============================================================
// Example 1: Record audio from microphone to WAV on SD card
// ============================================================

static void record_wav(const char *path, int duration_sec)
{
    Serial.printf("Recording %d seconds to %s ...\n", duration_sec, path);

    if (!init_i2s_recording()) return;
    speaker_off();

    File f = SD_MMC.open(path, FILE_WRITE);
    if (!f) {
        Serial.println("Failed to open file for writing");
        return;
    }

    // Write placeholder header (updated at end)
    WavHeader hdr;
    f.write((const uint8_t *)&hdr, sizeof(hdr));

    uint8_t buf[AUDIO_BUF_SIZE];
    uint32_t total_bytes = 0;
    uint32_t target_bytes = (uint32_t)SAMPLE_RATE * NUM_CHANNELS * (BITS_PER_SAMPLE / 8) * duration_sec;

    unsigned long start = millis();
    while (total_bytes < target_bytes) {
        size_t bytes_read = i2s.readBytes((char *)buf, sizeof(buf));
        if (bytes_read > 0) {
            f.write(buf, bytes_read);
            total_bytes += bytes_read;
        }

        // Progress every second
        static unsigned long last_print = 0;
        unsigned long now = millis();
        if (now - last_print >= 1000) {
            Serial.printf("  Recording... %u / %u bytes (%.1f s)\n",
                          total_bytes, target_bytes,
                          (float)(now - start) / 1000.0f);
            last_print = now;
        }
    }

    // Update WAV header with actual data size
    wav_header_write(f, total_bytes);
    f.close();

    Serial.printf("Recording complete: %u bytes (%.1f s)\n",
                  total_bytes + (uint32_t)sizeof(WavHeader),
                  (float)total_bytes / (SAMPLE_RATE * NUM_CHANNELS * (BITS_PER_SAMPLE / 8)));
}

// ============================================================
// Example 2: Play back a WAV file from SD card
// ============================================================

static void play_wav(const char *path)
{
    Serial.printf("Playing %s ...\n", path);

    File f = SD_MMC.open(path, FILE_READ);
    if (!f) {
        Serial.println("Failed to open file for reading");
        return;
    }

    // Read and validate WAV header
    WavHeader hdr;
    if (f.read((uint8_t *)&hdr, sizeof(hdr)) != sizeof(hdr)) {
        Serial.println("Failed to read WAV header");
        f.close();
        return;
    }

    if (memcmp(hdr.riff, "RIFF", 4) != 0 || memcmp(hdr.wave, "WAVE", 4) != 0) {
        Serial.println("Not a valid WAV file");
        f.close();
        return;
    }

    Serial.printf("  Format: %d Hz, %d-bit, %d ch, %u data bytes\n",
                  hdr.sample_rate, hdr.bits_per_samp, hdr.num_channels, hdr.data_size);

    if (!init_i2s_playback()) { f.close(); return; }
    speaker_on();

    uint8_t buf[AUDIO_BUF_SIZE];
    uint32_t remaining = hdr.data_size;
    unsigned long start = millis();

    while (remaining > 0) {
        size_t to_read = remaining < sizeof(buf) ? remaining : sizeof(buf);
        size_t bytes_read = f.read(buf, to_read);
        if (bytes_read == 0) break;

        i2s.write(buf, bytes_read);
        remaining -= bytes_read;
    }

    unsigned long elapsed = millis() - start;
    Serial.printf("Playback complete (%.1f s)\n", (float)elapsed / 1000.0f);

    speaker_off();
    f.close();
}

// ============================================================
// Example 3: Generate and play a test tone (no SD card needed)
// ============================================================

static void play_test_tone(float freq_hz, int duration_ms)
{
    Serial.printf("Playing %.0f Hz test tone for %d ms...\n", freq_hz, duration_ms);

    if (!init_i2s_playback()) return;
    speaker_on();

    uint32_t total_samples = (uint32_t)SAMPLE_RATE * duration_ms / 1000;
    int16_t buf[256];

    for (uint32_t written = 0; written < total_samples; ) {
        uint32_t chunk = total_samples - written;
        if (chunk > 256) chunk = 256;

        for (uint32_t i = 0; i < chunk; i++) {
            float t = (float)(written + i) / SAMPLE_RATE;
            buf[i] = (int16_t)(16000.0f * sinf(2.0f * M_PI * freq_hz * t));
        }
        i2s.write((uint8_t *)buf, chunk * sizeof(int16_t));
        written += chunk;
    }

    delay(50);
    speaker_off();
    Serial.println("Tone complete");
}

// ============================================================
// Example 4: Generate and save a test tone as WAV on SD card
// ============================================================

static void generate_wav(const char *path, float freq_hz, int duration_ms)
{
    Serial.printf("Generating %.0f Hz tone -> %s ...\n", freq_hz, path);

    File f = SD_MMC.open(path, FILE_WRITE);
    if (!f) {
        Serial.println("Failed to open file for writing");
        return;
    }

    // Placeholder header
    WavHeader hdr;
    f.write((const uint8_t *)&hdr, sizeof(hdr));

    uint32_t total_samples = (uint32_t)SAMPLE_RATE * duration_ms / 1000;
    int16_t buf[256];
    uint32_t total_bytes = 0;

    for (uint32_t written = 0; written < total_samples; ) {
        uint32_t chunk = total_samples - written;
        if (chunk > 256) chunk = 256;

        for (uint32_t i = 0; i < chunk; i++) {
            float t = (float)(written + i) / SAMPLE_RATE;
            buf[i] = (int16_t)(16000.0f * sinf(2.0f * M_PI * freq_hz * t));
        }
        size_t bytes = chunk * sizeof(int16_t);
        f.write((const uint8_t *)buf, bytes);
        total_bytes += bytes;
        written += chunk;
    }

    wav_header_write(f, total_bytes);
    f.close();
    Serial.printf("Generated: %u bytes\n", total_bytes + (uint32_t)sizeof(WavHeader));
}

// ============================================================
// Example 5: List WAV files on SD card
// ============================================================

static void list_wav_files(const char *dir_path)
{
    File dir = SD_MMC.open(dir_path);
    if (!dir || !dir.isDirectory()) {
        Serial.printf("Cannot open directory: %s\n", dir_path);
        return;
    }

    Serial.printf("WAV files in %s:\n", dir_path);
    int count = 0;
    File entry;
    while ((entry = dir.openNextFile())) {
        String name = entry.name();
        if (!entry.isDirectory() && name.endsWith(".wav")) {
            Serial.printf("  %s  (%u bytes)\n", entry.path(), entry.size());
            count++;
        }
        entry.close();
    }
    dir.close();

    if (count == 0)
        Serial.println("  (none)");
}

// ============================================================
// E-Paper display demo (from previous setup)
// ============================================================

#define EPD_WIDTH  EPD_3IN97_WIDTH
#define EPD_HEIGHT EPD_3IN97_HEIGHT
#define FB_STRIDE  ((EPD_WIDTH + 7) / 8)
#define FB_SIZE    (FB_STRIDE * EPD_HEIGHT)

static uint8_t *fb;

static void fb_clear(uint8_t color) { memset(fb, color ? 0xFF : 0x00, FB_SIZE); }

static void fb_set_pixel(int x, int y, bool white)
{
    if (x < 0 || x >= EPD_WIDTH || y < 0 || y >= EPD_HEIGHT) return;
    uint32_t idx = y * FB_STRIDE + x / 8;
    uint8_t mask = 0x80 >> (x % 8);
    if (white) fb[idx] |= mask;
    else       fb[idx] &= ~mask;
}

static void fb_fill_rect(int x0, int y0, int w, int h, bool white)
{
    for (int y = y0; y < y0 + h && y < EPD_HEIGHT; y++)
        for (int x = x0; x < x0 + w && x < EPD_WIDTH; x++)
            fb_set_pixel(x, y, white);
}

static void fb_draw_rect(int x0, int y0, int w, int h, int thick, bool white)
{
    fb_fill_rect(x0, y0, w, thick, white);
    fb_fill_rect(x0, y0 + h - thick, w, thick, white);
    fb_fill_rect(x0, y0, thick, h, white);
    fb_fill_rect(x0 + w - thick, y0, thick, h, white);
}

static const uint8_t FONT_5X7[][5] = {
    {0x00,0x00,0x00,0x00,0x00},
    {0x7E,0x11,0x11,0x11,0x7E},{0x7F,0x49,0x49,0x49,0x36},
    {0x3E,0x41,0x41,0x41,0x22},{0x7F,0x41,0x41,0x22,0x1C},
    {0x7F,0x49,0x49,0x49,0x41},{0x7F,0x09,0x09,0x09,0x01},
    {0x3E,0x41,0x49,0x49,0x7A},{0x7F,0x08,0x08,0x08,0x7F},
    {0x00,0x41,0x7F,0x41,0x00},{0x20,0x40,0x41,0x3F,0x01},
    {0x7F,0x08,0x14,0x22,0x41},{0x7F,0x40,0x40,0x40,0x40},
    {0x7F,0x02,0x0C,0x02,0x7F},{0x7F,0x04,0x08,0x10,0x7F},
    {0x3E,0x41,0x41,0x41,0x3E},{0x7F,0x09,0x09,0x09,0x06},
    {0x3E,0x41,0x51,0x21,0x5E},{0x7F,0x09,0x19,0x29,0x46},
    {0x46,0x49,0x49,0x49,0x31},{0x01,0x01,0x7F,0x01,0x01},
    {0x3F,0x40,0x40,0x40,0x3F},{0x1F,0x20,0x40,0x20,0x1F},
    {0x3F,0x40,0x38,0x40,0x3F},{0x63,0x14,0x08,0x14,0x63},
    {0x07,0x08,0x70,0x08,0x07},{0x61,0x51,0x49,0x45,0x43},
    {0x3E,0x51,0x49,0x45,0x3E},{0x00,0x42,0x7F,0x40,0x00},
    {0x42,0x61,0x51,0x49,0x46},{0x21,0x41,0x45,0x4B,0x31},
    {0x18,0x14,0x12,0x7F,0x10},{0x27,0x45,0x45,0x45,0x39},
    {0x3C,0x4A,0x49,0x49,0x30},{0x01,0x71,0x09,0x05,0x03},
    {0x36,0x49,0x49,0x49,0x36},{0x06,0x49,0x49,0x29,0x1E},
    {0x14,0x14,0x14,0x14,0x14},{0x36,0x36,0x00,0x00,0x00},
    {0x00,0x60,0x60,0x00,0x00},
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

static void fb_draw_text(int x, int y, const char *str, int scale, bool white)
{
    while (*str) {
        int idx = font_index(*str);
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
        x += 6 * scale;
        str++;
    }
}

static void show_epaper_status(const char *line1, const char *line2, const char *line3)
{
    if (!fb) return;

    fb_clear(true);
    fb_draw_rect(0, 0, EPD_WIDTH, EPD_HEIGHT, 3, false);

    fb_draw_text(30, 20, "ESP32-S3 EPAPER 3.97", 4, false);
    fb_fill_rect(20, 60, EPD_WIDTH - 40, 2, false);

    if (line1) fb_draw_text(30, 80,  line1, 3, false);
    if (line2) fb_draw_text(30, 120, line2, 3, false);
    if (line3) fb_draw_text(30, 160, line3, 3, false);

    fb_draw_text(30, 380, "SEND COMMANDS VIA SERIAL", 2, false);
    fb_draw_text(30, 410, "R:RECORD P:PLAY T:TONE", 2, false);
    fb_draw_text(30, 440, "G:GENERATE L:LIST", 2, false);

    EPD_3IN97_Display_Fast(fb);
}

// ============================================================
// Serial menu
// ============================================================

static void print_menu()
{
    Serial.println();
    Serial.println("========================================");
    Serial.println("  ESP32-S3-ePaper-3.97 Audio Demo");
    Serial.println("========================================");
    Serial.println("  r - Record 5s from mic -> /sdcard/recording.wav");
    Serial.println("  p - Play /sdcard/recording.wav");
    Serial.println("  t - Play 440 Hz test tone (speaker)");
    Serial.println("  g - Generate 1 kHz tone -> /sdcard/tone.wav");
    Serial.println("  l - List WAV files on SD card");
    Serial.println("  d - Show e-Paper display demo");
    Serial.println("========================================");
    Serial.println("Send a letter + Enter:");
}

// ============================================================
// Setup & Loop
// ============================================================

void setup()
{
    Serial.begin(115200);
    delay(2000);
    Serial.println("\n=== ESP32-S3-ePaper-3.97 Audio + Display Demo ===");

    // Power amplifier control
    pinMode(PA_CTRL, OUTPUT);
    speaker_off();

    // Init SD card
    sd_ok = init_sd_card();

    // Init audio codec
    codec_ok = init_codec();

    // Init I2S for playback by default
    if (codec_ok)
        init_i2s_playback();

    // Init e-Paper and show status
    fb = (uint8_t *)ps_malloc(FB_SIZE);
    if (fb) {
        DEV_Module_Init();
        EPD_3IN97_Init_Fast();
        show_epaper_status(
            sd_ok    ? "SD CARD: OK"    : "SD CARD: FAIL",
            codec_ok ? "AUDIO:   OK"    : "AUDIO:   FAIL",
            "READY"
        );
        EPD_3IN97_Sleep();
    }

    print_menu();
}

void loop()
{
    if (!Serial.available()) return;

    char cmd = Serial.read();
    // Flush remaining characters (e.g. newline)
    while (Serial.available()) Serial.read();

    switch (cmd) {
    case 'r':
    case 'R':
        if (!sd_ok || !codec_ok) {
            Serial.println("SD card or codec not available");
            break;
        }
        record_wav("/sdcard/recording.wav", 5);
        break;

    case 'p':
    case 'P':
        if (!sd_ok || !codec_ok) {
            Serial.println("SD card or codec not available");
            break;
        }
        play_wav("/sdcard/recording.wav");
        break;

    case 't':
    case 'T':
        if (!codec_ok) {
            Serial.println("Codec not available");
            break;
        }
        play_test_tone(440.0f, 2000);
        break;

    case 'g':
    case 'G':
        if (!sd_ok) {
            Serial.println("SD card not available");
            break;
        }
        generate_wav("/sdcard/tone.wav", 1000.0f, 3000);
        Serial.println("Now play it with 'p' after renaming, or modify play_wav path");
        break;

    case 'l':
    case 'L':
        if (!sd_ok) {
            Serial.println("SD card not available");
            break;
        }
        list_wav_files("/sdcard");
        break;

    case 'd':
    case 'D':
        if (!fb) {
            Serial.println("Display framebuffer not allocated");
            break;
        }
        Serial.println("Refreshing e-Paper...");
        EPD_3IN97_Init_Fast();
        show_epaper_status(
            sd_ok    ? "SD CARD: OK"    : "SD CARD: FAIL",
            codec_ok ? "AUDIO:   OK"    : "AUDIO:   FAIL",
            "READY"
        );
        EPD_3IN97_Sleep();
        Serial.println("Display updated");
        break;

    case '\n':
    case '\r':
        break;

    default:
        print_menu();
        break;
    }
}
