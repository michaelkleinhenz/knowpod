#include "audio.h"
#include <Wire.h>
#include <esp_heap_caps.h>
#include "board.h"
#include "es8311.h"
#ifdef EXIO_PA_CTRL
#include "hw/exio.h"
#endif
#ifdef REC_MP3
extern "C" {
#include <layer3.h>
}
#endif

#define MCLK_MULTIPLE    256
#define MCLK_FREQ_HZ     (SAMPLE_RATE * MCLK_MULTIPLE)
#define VOICE_VOLUME     70
#define CUE_VOLUME       95      // codec volume for signal tones (0-100)
#define CUE_AMPLITUDE    30000   // near full scale
#define CUE_FADE_MS      8       // fade in/out against clicks
#define CUE_GAP_MS       60

I2SClass i2s;
static es8311_handle_t codec = nullptr;

// ============================================================
// Codec & I2S
// ============================================================

static bool rec_stream_init();
static void log_heap(const char *when);

bool audio_begin()
{
    es8311_handle_t es = codec = es8311_create(I2C_NUM_0, ES8311_ADDRRES_0);
    if (!es) {
        Serial.println("ES8311 create FAILED");
        return false;
    }

    const es8311_clock_config_t clk = {
        .mclk_inverted      = false,
        .sclk_inverted      = false,
        .mclk_from_mclk_pin = true,
        .mclk_frequency     = MCLK_FREQ_HZ,
        .sample_frequency   = SAMPLE_RATE
    };

    if (es8311_init(es, &clk, ES8311_RESOLUTION_16, ES8311_RESOLUTION_16) != ESP_OK) {
        Serial.println("ES8311 init FAILED");
        return false;
    }
    es8311_voice_volume_set(es, VOICE_VOLUME, NULL);
    es8311_microphone_config(es, false);
    es8311_microphone_gain_set(es, ES8311_MIC_GAIN_42DB);

#ifdef PIN_PA_CTRL
    pinMode(PIN_PA_CTRL, OUTPUT);
#endif
    speaker_off();

    Serial.println("ES8311 codec initialized");
    // The recording buffer is taken now, before Wi-Fi or Bluetooth leave the heap in
    // pieces, and kept: later a block this large may no longer be free
    if (!rec_stream_init()) Serial.println("Failed to allocate recording buffer");
    log_heap("after audio setup");
    return audio_i2s_begin();
}

bool audio_i2s_begin()
{
    i2s.end();
    i2s.setPins(PIN_I2S_BCK, PIN_I2S_LRCK, PIN_I2S_DOUT, PIN_I2S_DIN, PIN_I2S_MCK);
    if (!i2s.begin(I2S_MODE_STD, SAMPLE_RATE, I2S_DATA_BIT_WIDTH_16BIT,
                   I2S_SLOT_MODE_MONO, I2S_STD_SLOT_LEFT)) {
        Serial.println("I2S init FAILED");
        return false;
    }
    return true;
}

#ifdef EXIO_PA_CTRL
void speaker_on()  { exio_set(EXIO_PA_CTRL, true); }
void speaker_off() { exio_set(EXIO_PA_CTRL, false); }
#else
void speaker_on()  { digitalWrite(PIN_PA_CTRL, HIGH); }
void speaker_off() { digitalWrite(PIN_PA_CTRL, LOW);  }
#endif

static void wav_header_write(File &f, uint32_t data_bytes)
{
    WavHeader hdr;
    hdr.set_data_size(data_bytes);
    f.seek(0);
    f.write((const uint8_t *)&hdr, sizeof(hdr));
}

// ============================================================
// Recording
//
// A capture task reads the mic into a large buffer while recorder_poll()
// writes it to the SD card in big chunks, so SD write stalls (100+ ms) and
// e-ink refreshes (up to ~3 s) don't drop audio. With REC_MP3 the capture
// task encodes each MP3 frame before buffering it, so the buffer holds
// compressed audio and the file needs no header.
// ============================================================

#if defined(REC_MP3) && defined(BOARD_HAS_PSRAM)
#define REC_BUFFER_BYTES     (128 * 1024)   // ~32 s of 32 kbps MP3
#define REC_WRITE_CHUNK      (16 * 1024)
#elif defined(REC_MP3)
#define REC_BUFFER_BYTES     (32 * 1024)    // ~8 s of 32 kbps MP3
#define REC_WRITE_CHUNK      (4 * 1024)
#elif defined(BOARD_HAS_PSRAM)
#define REC_BUFFER_BYTES     (512 * 1024)   // ~16 s of audio
#define REC_WRITE_CHUNK      (16 * 1024)
#else
#define REC_BUFFER_BYTES     (96 * 1024)    // ~3 s of audio; full refreshes take ~1.5 s
#define REC_WRITE_CHUNK      (4 * 1024)
#endif
#ifdef REC_MP3
#define CAPTURE_STACK        6144           // the encoder runs in the capture task
#else
#define CAPTURE_STACK        4096
#endif
#define REC_CHECKPOINT_MS    5000
#define WAV_MAX_DATA_BYTES   (0xFFFFFFFFu - sizeof(WavHeader))  // ~37 h at 16 kHz

static StreamBufferHandle_t rec_stream;
static volatile bool        rec_stop    = false;
static volatile bool        rec_running = false;
static volatile uint32_t    rec_dropped = 0;   // PCM bytes lost because the buffer was full

static File     rec_file;
static bool     rec_active = false;
static bool     rec_failed = false;
static uint32_t rec_bytes  = 0;
static uint32_t rec_last_checkpoint = 0;

// Sends whole blocks only, so WAV samples and MP3 frames are never split.
static bool rec_buffer(const void *data, size_t n)
{
    if (xStreamBufferSpacesAvailable(rec_stream) < n) return false;
    xStreamBufferSend(rec_stream, data, n, 0);
    return true;
}

#ifdef REC_MP3
// MPEG-2 Layer III, mono. Frames hold 576 samples (36 ms) at 16 kHz.
static shine_t  mp3 = nullptr;
static int16_t *mp3_pcm = nullptr;          // the frame being filled
static int      mp3_frame = 0;              // samples per frame
static int      mp3_fill = 0;
static volatile uint32_t rec_samples = 0;   // samples encoded into the buffer
static uint32_t mp3_frames = 0, mp3_total_us = 0, mp3_max_us = 0;

static bool mp3_open()
{
    if (shine_check_config(SAMPLE_RATE, MP3_BITRATE_KBPS) < 0) return false;
    shine_config_t cfg;
    shine_set_config_mpeg_defaults(&cfg.mpeg);
    cfg.wave.channels = PCM_MONO;
    cfg.wave.samplerate = SAMPLE_RATE;
    cfg.mpeg.mode = MONO;
    cfg.mpeg.bitr = MP3_BITRATE_KBPS;
    if (!mp3_pcm) mp3_pcm = (int16_t *)malloc(SHINE_MAX_SAMPLES * sizeof(int16_t));
    if (!mp3_pcm || !(mp3 = shine_initialise(&cfg))) return false;
    mp3_frame = shine_samples_per_pass(mp3);
    mp3_fill = 0;
    rec_samples = 0;
    mp3_frames = mp3_total_us = mp3_max_us = 0;
    return true;
}

// Encodes the full frame in mp3_pcm and buffers the result.
static void mp3_encode_frame()
{
    uint32_t start = micros();
    int16_t *channels[1] = {mp3_pcm};
    int n = 0;
    unsigned char *out = shine_encode_buffer(mp3, channels, &n);
    uint32_t us = micros() - start;
    mp3_frames++;
    mp3_total_us += us;
    if (us > mp3_max_us) mp3_max_us = us;

    if (n <= 0 || rec_buffer(out, n)) rec_samples += mp3_frame;
    else rec_dropped += mp3_frame * sizeof(int16_t);
}

static void mp3_add_samples(const int16_t *pcm, size_t count)
{
    while (count > 0) {
        size_t take = min<size_t>(count, mp3_frame - mp3_fill);
        memcpy(mp3_pcm + mp3_fill, pcm, take * sizeof(int16_t));
        mp3_fill += take;
        pcm += take;
        count -= take;
        if (mp3_fill == mp3_frame) {
            mp3_encode_frame();
            mp3_fill = 0;
        }
    }
}
#endif

static void capture_task(void *)
{
    static uint8_t buf[AUDIO_BUF_SIZE];
    while (!rec_stop) {
        size_t n = i2s.readBytes((char *)buf, sizeof(buf));
        if (n == 0) continue;
#ifdef REC_MP3
        mp3_add_samples((const int16_t *)buf, n / sizeof(int16_t));
#else
        if (!rec_buffer(buf, n)) rec_dropped += n;
#endif
    }
    rec_running = false;
    vTaskDelete(nullptr);
}

static bool rec_stream_init()
{
    if (rec_stream) {
        xStreamBufferReset(rec_stream);
        return true;
    }
    static StaticStreamBuffer_t sb;
#ifdef BOARD_HAS_PSRAM
    uint8_t *storage = (uint8_t *)ps_malloc(REC_BUFFER_BYTES + 1);
#else
    uint8_t *storage = (uint8_t *)malloc(REC_BUFFER_BYTES + 1);
#endif
    if (!storage) return false;
    rec_stream = xStreamBufferCreateStatic(REC_BUFFER_BYTES, 1, storage, &sb);
    return rec_stream != nullptr;
}

static void log_heap(const char *when)
{
    Serial.printf("Heap %s: %u free, largest block %u\n", when,
                  (unsigned)heap_caps_get_free_size(MALLOC_CAP_8BIT),
                  (unsigned)heap_caps_get_largest_free_block(MALLOC_CAP_8BIT));
}

bool recorder_start(fs::FS &fs, const char *path)
{
    if (rec_active) return false;
    log_heap("at recording start");
    if (!rec_stream_init()) {
        Serial.println("Failed to allocate recording buffer");
        return false;
    }
    if (!audio_i2s_begin()) return false;
    speaker_off();
#ifdef REC_MP3
    if (!mp3_open()) {
        Serial.println("Failed to start the MP3 encoder (out of memory?)");
        return false;
    }
#endif

    rec_file = fs.open(path, FILE_WRITE);
    if (!rec_file) {
        Serial.printf("Failed to open %s for writing\n", path);
#ifdef REC_MP3
        shine_close(mp3);
        mp3 = nullptr;
#endif
        return false;
    }

#ifndef REC_MP3
    // Placeholder header, updated at checkpoints and at the end
    WavHeader hdr;
    rec_file.write((const uint8_t *)&hdr, sizeof(hdr));
#endif

    rec_bytes = 0;
    rec_failed = false;
    rec_dropped = 0;
    rec_last_checkpoint = millis();
    rec_stop = false;
    rec_running = true;
    rec_active = true;
    xTaskCreatePinnedToCore(capture_task, "capture", CAPTURE_STACK, nullptr, 5, nullptr, LOOP_CORE);

    Serial.printf("Recording to %s\n", path);
    return true;
}

static bool rec_write(const uint8_t *data, size_t n)
{
    if (n > WAV_MAX_DATA_BYTES - rec_bytes) n = WAV_MAX_DATA_BYTES - rec_bytes;
    if (rec_file.write(data, n) != n) {
        Serial.println("SD write FAILED (card full?)");
        rec_failed = true;
        return false;
    }
    rec_bytes += n;
    return true;
}

// Writes buffered audio to the file; returns false if nothing was pending.
static bool drain(TickType_t wait)
{
    static uint8_t chunk[REC_WRITE_CHUNK];
    size_t n = xStreamBufferReceive(rec_stream, chunk, sizeof(chunk), wait);
    return n > 0 && rec_write(chunk, n);
}

void recorder_poll()
{
    if (!rec_active || rec_failed) return;

    // Keep up with the capture task without blocking the UI for long
    for (int i = 0; i < 8 && drain(0); i++) {}

    if (rec_bytes >= WAV_MAX_DATA_BYTES) rec_failed = true;  // file size limit

    if (millis() - rec_last_checkpoint >= REC_CHECKPOINT_MS) {
#ifndef REC_MP3
        size_t pos = rec_file.position();
        wav_header_write(rec_file, rec_bytes);
        rec_file.seek(pos);
#endif
        rec_file.flush();
        rec_last_checkpoint = millis();
    }
}

void recorder_stop()
{
    if (!rec_active) return;
    rec_stop = true;
    while (rec_running) delay(5);
    while (!rec_failed && drain(0)) {}

#ifdef REC_MP3
    // The last partial frame (padded with silence) and the encoder's pending bits
    if (mp3_fill > 0) {
        memset(mp3_pcm + mp3_fill, 0, (mp3_frame - mp3_fill) * sizeof(int16_t));
        mp3_encode_frame();
        while (!rec_failed && drain(0)) {}
    }
    int n = 0;
    unsigned char *out = shine_flush(mp3, &n);
    if (n > 0 && !rec_failed) rec_write(out, n);
    shine_close(mp3);
    mp3 = nullptr;
    float frame_ms = 1000.0f * mp3_frame / SAMPLE_RATE;
    float avg_ms = mp3_frames ? mp3_total_us / 1000.0f / mp3_frames : 0;
    Serial.printf("MP3: %u frames, encoding took %.1f ms on average (max %.1f ms) per %.0f ms frame = %.0f%% CPU\n",
                  (unsigned)mp3_frames, avg_ms, mp3_max_us / 1000.0f, frame_ms, 100 * avg_ms / frame_ms);
#else
    wav_header_write(rec_file, rec_bytes);
#endif
    rec_file.close();
    rec_active = false;

    Serial.printf("Recording complete: %.1f s, %u bytes\n", recorder_seconds(), (unsigned)rec_bytes);
    if (rec_dropped)
        Serial.printf("WARNING: %.1f s of audio dropped (SD card too slow)\n",
                      recorder_dropped_seconds());
}

bool recorder_active()             { return rec_active; }
bool recorder_failed()             { return rec_failed; }
#ifdef REC_MP3
float recorder_seconds()           { return (float)rec_samples / SAMPLE_RATE; }
#else
float recorder_seconds()           { return (float)rec_bytes / BYTES_PER_SEC; }
#endif
float recorder_dropped_seconds()   { return (float)rec_dropped / BYTES_PER_SEC; }

bool record_wav(fs::FS &fs, const char *path, int duration_sec,
                const std::function<bool()> &should_stop)
{
    if (!recorder_start(fs, path)) return false;

    unsigned long last_print = millis();
    while (!recorder_failed() && !should_stop() &&
           (duration_sec <= 0 || recorder_seconds() < duration_sec)) {
        recorder_poll();
        if (millis() - last_print >= 5000) {
            last_print = millis();
            Serial.printf("  Recording... %.0f s\n", recorder_seconds());
        }
        delay(20);
    }
    bool ok = !recorder_failed();
    recorder_stop();
    return ok;
}

// ============================================================
// Playback
// ============================================================

void play_wav(fs::FS &fs, const char *path, const std::function<bool()> &should_stop)
{
    Serial.printf("Playing %s ...\n", path);

    File f = fs.open(path, FILE_READ);
    if (!f) {
        Serial.println("Failed to open file for reading");
        return;
    }

    WavHeader hdr;
    if (f.read((uint8_t *)&hdr, sizeof(hdr)) != sizeof(hdr) ||
        memcmp(hdr.riff, "RIFF", 4) != 0 || memcmp(hdr.wave, "WAVE", 4) != 0) {
        Serial.println("Not a valid WAV file");
        f.close();
        return;
    }

    Serial.printf("  Format: %d Hz, %d-bit, %d ch, %u data bytes\n",
                  hdr.sample_rate, hdr.bits_per_samp, hdr.num_channels, hdr.data_size);

    if (!audio_i2s_begin()) { f.close(); return; }
    speaker_on();

    uint8_t buf[AUDIO_BUF_SIZE];
    uint32_t remaining = hdr.data_size;
    unsigned long start = millis();

    while (remaining > 0 && !(should_stop && should_stop())) {
        size_t to_read = remaining < sizeof(buf) ? remaining : sizeof(buf);
        size_t bytes_read = f.read(buf, to_read);
        if (bytes_read == 0) break;
        i2s.write(buf, bytes_read);
        remaining -= bytes_read;
    }

    Serial.printf("Playback complete (%.1f s)\n", (millis() - start) / 1000.0f);
    speaker_off();
    f.close();
}

void play_test_tone(float freq_hz, int duration_ms)
{
    Serial.printf("Playing %.0f Hz test tone for %d ms...\n", freq_hz, duration_ms);

    if (!audio_i2s_begin()) return;
    speaker_on();
#ifdef EXIO_PA_CTRL
    uint8_t outputs = 0, config = 0;
    if (exio_read(outputs, config))
        Serial.printf("  TCA9554: outputs 0x%02X, config 0x%02X (amplifier EXIO%d %s)\n", outputs, config,
                      EXIO_PA_CTRL, (outputs >> EXIO_PA_CTRL) & 1 ? "on" : "OFF");
    else
        Serial.println("  TCA9554: read FAILED");
#endif

    uint32_t total_samples = (uint32_t)SAMPLE_RATE * duration_ms / 1000;
    int16_t buf[256];
    size_t accepted = 0;
    uint32_t start = millis();

    for (uint32_t written = 0; written < total_samples; ) {
        uint32_t chunk = total_samples - written;
        if (chunk > 256) chunk = 256;
        for (uint32_t i = 0; i < chunk; i++) {
            float t = (float)(written + i) / SAMPLE_RATE;
            buf[i] = (int16_t)(16000.0f * sinf(2.0f * M_PI * freq_hz * t));
        }
        accepted += i2s.write((uint8_t *)buf, chunk * sizeof(int16_t));
        written += chunk;
    }

    // I2S accepts samples at the sample rate, so this takes about duration_ms;
    // much faster or fewer bytes means the I2S clocks are not running
    Serial.printf("  I2S accepted %u of %u bytes in %lu ms\n", (unsigned)accepted,
                  (unsigned)(total_samples * sizeof(int16_t)), (unsigned long)(millis() - start));
    delay(50);
    speaker_off();
    Serial.println("Tone complete");
}

// ============================================================
// Signal tones
// ============================================================

// Writes a sine tone with short fades; `ms` of silence if freq_hz is 0.
static void write_tone(float freq_hz, int ms)
{
    uint32_t total = (uint32_t)SAMPLE_RATE * ms / 1000;
    uint32_t fade = (uint32_t)SAMPLE_RATE * CUE_FADE_MS / 1000;
    int16_t buf[256];
    for (uint32_t done = 0; done < total;) {
        uint32_t n = min<uint32_t>(total - done, 256);
        for (uint32_t i = 0; i < n; i++) {
            uint32_t k = done + i;
            float env = 1.0f;
            if (k < fade) env = (float)k / fade;
            else if (total - k < fade) env = (float)(total - k) / fade;
            float v = freq_hz > 0 ? sinf(2.0f * M_PI * freq_hz * k / SAMPLE_RATE) : 0.0f;
            buf[i] = (int16_t)(CUE_AMPLITUDE * env * v);
        }
        i2s.write((uint8_t *)buf, n * sizeof(int16_t));
        done += n;
    }
}

void play_cue(Cue cue)
{
    if (!codec || !audio_i2s_begin()) return;
    es8311_voice_volume_set(codec, CUE_VOLUME, NULL);
    speaker_on();
    write_tone(0, 20);  // let the amplifier settle
    if (cue == CUE_START) {
        write_tone(1500, 110);
        write_tone(0, CUE_GAP_MS);
        write_tone(2000, 160);
    } else {
        write_tone(2000, 110);
        write_tone(0, CUE_GAP_MS);
        write_tone(1500, 110);
        write_tone(0, CUE_GAP_MS);
        write_tone(1000, 220);
    }
    write_tone(0, 60);  // flush the DMA buffers before muting
    speaker_off();
    es8311_voice_volume_set(codec, VOICE_VOLUME, NULL);
}
