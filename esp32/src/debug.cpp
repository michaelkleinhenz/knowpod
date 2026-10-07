#include "debug.h"
#include <SD_MMC.h>
#include "audio/audio.h"
#include "net/openrouter.h"
#include "net/wifi.h"
#include "session.h"

// Serial console commands for testing audio and OpenRouter without the UI.

#define TEST_RECORDING "/recording.wav"

static void print_menu();

// ============================================================
// List WAV files on the SD card
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
// Transcribe a WAV file on the SD card
// ============================================================

static void transcribe_file(const char *path)
{
    if (!wifi_connect()) return;

    File f = SD_MMC.open(path, FILE_READ);
    if (!f) {
        Serial.printf("Failed to open %s\n", path);
        return;
    }

    if (f.size() > 25 * 1024 * 1024) {
        Serial.println("File is larger than OpenRouter's 25 MB upload limit");
        f.close();
        return;
    }

    Serial.printf("Transcribing %s (%u bytes) ...\n", path, (unsigned)f.size());
    unsigned long start = millis();

    JsonDocument resp;
    ApiResult r = openrouter_transcribe(f.size(), [&f](Print &out) {
        uint8_t buf[4096];
        size_t n;
        while ((n = f.read(buf, sizeof(buf))) > 0)
            if (out.write(buf, n) != n) return false;
        return true;
    }, false, resp);
    f.close();

    if (r.ok)
        Serial.printf("Transcript (%.1f s): %s\n", (millis() - start) / 1000.0f, (const char *)(resp["text"] | ""));
    else
        Serial.printf("Transcription FAILED: %s\n", r.error.c_str());
}

// ============================================================
// Continuous transcription
//
// A recorder task reads the mic in 20 ms frames and uses a simple
// energy-based voice activity detector to cut speech into segments
// (utterances ending in a pause). Finished segments are queued and
// uploaded from loop() while the recorder keeps listening.
// ============================================================

#define VAD_FRAME_MS        20
#define VAD_FRAME_SAMPLES   (SAMPLE_RATE * VAD_FRAME_MS / 1000)
#define VAD_CALIBRATE_MS    500     // initial noise floor measurement
#define VAD_PREROLL_MS      300     // audio kept from before speech onset
#define VAD_ONSET_MS        60      // speech needed to start a segment
#define VAD_HANGOVER_MS     800     // silence that ends a segment
#define VAD_MIN_SPEECH_MS   300     // shorter segments are discarded as noise
#define VAD_SNR             3.0f    // speech threshold relative to noise floor
#define VAD_MIN_RMS         200.0f  // absolute speech threshold floor
#define SEGMENT_MAX_SEC     15
#define SEGMENT_MAX_SAMPLES (SAMPLE_RATE * SEGMENT_MAX_SEC)
#define SEGMENT_POOL_SIZE   3

#define MS_TO_FRAMES(ms)    ((ms) / VAD_FRAME_MS)

struct Segment {
    int16_t *pcm;
    size_t   samples;
};

static int16_t      *segment_pool[SEGMENT_POOL_SIZE];
static QueueHandle_t free_segments;   // empty buffers (int16_t *)
static QueueHandle_t ready_segments;  // captured speech (Segment)
static volatile bool listen_stop    = false;
static volatile bool listen_running = false;
static bool          listening      = false;

static bool read_frame(int16_t *frame)
{
    size_t got = 0;
    while (got < VAD_FRAME_SAMPLES * sizeof(int16_t)) {
        if (listen_stop) return false;
        got += i2s.readBytes((char *)frame + got, VAD_FRAME_SAMPLES * sizeof(int16_t) - got);
    }
    return true;
}

static float frame_rms(const int16_t *frame)
{
    int32_t sum = 0;
    for (int i = 0; i < VAD_FRAME_SAMPLES; i++) sum += frame[i];
    float mean = (float)sum / VAD_FRAME_SAMPLES;  // remove DC offset

    float sq = 0;
    for (int i = 0; i < VAD_FRAME_SAMPLES; i++) {
        float s = frame[i] - mean;
        sq += s * s;
    }
    return sqrtf(sq / VAD_FRAME_SAMPLES);
}

static void listen_task(void *)
{
    static int16_t frame[VAD_FRAME_SAMPLES];
    static int16_t preroll[MS_TO_FRAMES(VAD_PREROLL_MS)][VAD_FRAME_SAMPLES];
    const int preroll_frames = MS_TO_FRAMES(VAD_PREROLL_MS);

    int preroll_head = 0, preroll_count = 0;
    Segment seg = {nullptr, 0};
    int onset = 0, silence = 0, speech = 0;
    unsigned long last_drop_msg = 0;

    // Calibrate the noise floor (assumes nobody talks right at the start)
    float noise = 0;
    int cal_frames = 0;
    while (cal_frames < MS_TO_FRAMES(VAD_CALIBRATE_MS) && read_frame(frame)) {
        noise += frame_rms(frame);
        cal_frames++;
    }
    if (cal_frames) noise /= cal_frames;
    if (!listen_stop)
        Serial.printf("Noise floor: %.0f RMS. Start speaking.\n", noise);

    while (read_frame(frame)) {
        float rms = frame_rms(frame);
        float threshold = max(noise * VAD_SNR, VAD_MIN_RMS);
        bool is_speech = rms > threshold;

        if (!seg.pcm) {
            // Idle: follow the noise floor (fast down, slow up) and keep a pre-roll
            noise = rms < noise ? 0.8f * noise + 0.2f * rms : 0.995f * noise + 0.005f * rms;

            memcpy(preroll[preroll_head], frame, sizeof(frame));
            preroll_head = (preroll_head + 1) % preroll_frames;
            if (preroll_count < preroll_frames) preroll_count++;

            onset = is_speech ? onset + 1 : 0;
            if (onset < MS_TO_FRAMES(VAD_ONSET_MS)) continue;

            if (xQueueReceive(free_segments, &seg.pcm, 0) != pdTRUE) {
                seg.pcm = nullptr;
                if (millis() - last_drop_msg > 2000) {
                    Serial.println("[transcription is falling behind, dropping audio]");
                    last_drop_msg = millis();
                }
                continue;
            }

            // Start the segment with the pre-roll (oldest frame first)
            seg.samples = 0;
            int idx = (preroll_head - preroll_count + preroll_frames) % preroll_frames;
            for (int i = 0; i < preroll_count; i++) {
                memcpy(seg.pcm + seg.samples, preroll[idx], sizeof(frame));
                seg.samples += VAD_FRAME_SAMPLES;
                idx = (idx + 1) % preroll_frames;
            }
            speech = onset;
            silence = 0;
            continue;
        }

        // In a segment
        memcpy(seg.pcm + seg.samples, frame, sizeof(frame));
        seg.samples += VAD_FRAME_SAMPLES;
        if (is_speech) { speech++; silence = 0; }
        else           { silence++; }

        bool full = seg.samples + VAD_FRAME_SAMPLES > SEGMENT_MAX_SAMPLES;
        if (silence < MS_TO_FRAMES(VAD_HANGOVER_MS) && !full) continue;

        if (speech >= MS_TO_FRAMES(VAD_MIN_SPEECH_MS))
            xQueueSend(ready_segments, &seg, portMAX_DELAY);
        else
            xQueueSend(free_segments, &seg.pcm, portMAX_DELAY);
        seg.pcm = nullptr;
        preroll_count = 0;
        onset = 0;
    }

    // Stopped: keep whatever speech was in progress
    if (seg.pcm) {
        if (speech >= MS_TO_FRAMES(VAD_MIN_SPEECH_MS))
            xQueueSend(ready_segments, &seg, portMAX_DELAY);
        else
            xQueueSend(free_segments, &seg.pcm, portMAX_DELAY);
    }

    listen_running = false;
    vTaskDelete(nullptr);
}

static void transcribe_segment(const Segment &seg)
{
    uint32_t data_bytes = seg.samples * sizeof(int16_t);
    unsigned long start = millis();

    JsonDocument resp;
    ApiResult r = openrouter_transcribe(sizeof(WavHeader) + data_bytes, [&](Print &out) {
        WavHeader hdr;
        hdr.set_data_size(data_bytes);
        out.write((const uint8_t *)&hdr, sizeof(hdr));
        return out.write((const uint8_t *)seg.pcm, data_bytes) == data_bytes;
    }, false, resp);
    String text = resp["text"] | "";
    text.trim();

    if (!r.ok)
        Serial.printf("Transcription FAILED: %s\n", r.error.c_str());
    else if (!text.isEmpty())
        Serial.printf("[%.1f s audio, %.1f s latency] %s\n",
                      (float)seg.samples / SAMPLE_RATE,
                      (millis() - start) / 1000.0f, text.c_str());
}

static bool start_listening()
{
    if (!wifi_connect()) return false;

    if (!free_segments) {
        free_segments  = xQueueCreate(SEGMENT_POOL_SIZE, sizeof(int16_t *));
        ready_segments = xQueueCreate(SEGMENT_POOL_SIZE, sizeof(Segment));
        for (int i = 0; i < SEGMENT_POOL_SIZE; i++) {
            segment_pool[i] = (int16_t *)ps_malloc(SEGMENT_MAX_SAMPLES * sizeof(int16_t));
            if (!segment_pool[i]) {
                Serial.println("Failed to allocate segment buffers");
                return false;
            }
            xQueueSend(free_segments, &segment_pool[i], 0);
        }
    }

    if (!audio_i2s_begin()) return false;
    speaker_off();

    listen_stop = false;
    listen_running = true;
    // Same core as loop() but higher priority, so uploads never starve the mic
    xTaskCreatePinnedToCore(listen_task, "listen", 4096, nullptr, 5, nullptr, 1);

    Serial.println("Continuous transcription started (send any key to stop).");
    Serial.println("Stay quiet for a moment while the noise floor is measured...");
    return true;
}

static void stop_listening()
{
    listen_stop = true;
    while (listen_running) delay(10);

    Segment seg;
    while (xQueueReceive(ready_segments, &seg, 0) == pdTRUE) {
        transcribe_segment(seg);
        xQueueSend(free_segments, &seg.pcm, 0);
    }
    Serial.println("Continuous transcription stopped.");
}

static void listen_loop()
{
    if (Serial.available()) {
        while (Serial.available()) Serial.read();
        stop_listening();
        listening = false;
        print_menu();
        return;
    }

    Segment seg;
    if (xQueueReceive(ready_segments, &seg, pdMS_TO_TICKS(50)) == pdTRUE) {
        transcribe_segment(seg);
        xQueueSend(free_segments, &seg.pcm, 0);
    }
}


// ============================================================
// Serial menu
// ============================================================

static void print_menu()
{
    Serial.println();
    Serial.println("========================================");
    Serial.println("  knowpod debug console");
    Serial.println("========================================");
    Serial.println("  r - Record from mic -> " TEST_RECORDING " (any key stops)");
    Serial.println("  p - Play " TEST_RECORDING);
    Serial.println("  t - Play 440 Hz test tone (speaker)");
    Serial.println("  l - List WAV files on SD card");
    Serial.println("  u - Transcribe " TEST_RECORDING " (OpenRouter)");
    Serial.println("  c - Continuous transcription (any key stops)");
    Serial.println("  w - Connect Wi-Fi");
    Serial.println("========================================");
}

bool debug_busy()
{
    return listening;
}

void debug_begin()
{
    print_menu();
}

void debug_loop()
{
    if (listening) {
        listen_loop();
        return;
    }

    if (!Serial.available()) return;

    char cmd = Serial.read();
    while (Serial.available()) Serial.read();  // drop the rest (e.g. newline)

    if (session_active() && strchr("rptuc", tolower(cmd))) {
        Serial.println("A recording is in progress; stop it with BOOT first.");
        return;
    }

    switch (tolower(cmd)) {
    case 'r':
        Serial.println("Recording to " TEST_RECORDING " (send any key to stop) ...");
        record_wav(SD_MMC, TEST_RECORDING, 0, [] {
            if (!Serial.available()) return false;
            while (Serial.available()) Serial.read();
            return true;
        });
        break;
    case 'p':
        play_wav(SD_MMC, TEST_RECORDING);
        break;
    case 't':
        play_test_tone(440.0f, 2000);
        break;
    case 'l':
        list_wav_files("/");
        break;
    case 'u':
        transcribe_file(TEST_RECORDING);
        break;
    case 'c':
        listening = start_listening();
        break;
    case 'w':
        wifi_connect();
        break;
    case '\n':
    case '\r':
        break;
    default:
        print_menu();
        break;
    }
}
