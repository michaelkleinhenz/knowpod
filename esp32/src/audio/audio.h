#pragma once

#include <Arduino.h>
#include <ESP_I2S.h>
#include <FS.h>
#include <functional>
#include "board.h"

#define SAMPLE_RATE      16000   // Whisper resamples to 16 kHz anyway
#define BITS_PER_SAMPLE  16
#define NUM_CHANNELS     1
#define BYTES_PER_SEC    (SAMPLE_RATE * NUM_CHANNELS * (BITS_PER_SAMPLE / 8))
#define AUDIO_BUF_SIZE   1024

// Recordings are 16 kHz mono WAV files, or MP3 on boards that define REC_MP3
#ifdef REC_MP3
#define REC_AUDIO_FILE          "audio.mp3"
#define REC_FILE_BYTES_PER_SEC  (MP3_BITRATE_KBPS * 1000 / 8)
#else
#define REC_AUDIO_FILE          "audio.wav"
#define REC_FILE_BYTES_PER_SEC  BYTES_PER_SEC
#endif

struct WavHeader {
    char     riff[4]       = {'R','I','F','F'};
    uint32_t file_size     = 0;
    char     wave[4]       = {'W','A','V','E'};
    char     fmt_id[4]     = {'f','m','t',' '};
    uint32_t fmt_size      = 16;
    uint16_t audio_format  = 1; // PCM
    uint16_t num_channels  = NUM_CHANNELS;
    uint32_t sample_rate   = SAMPLE_RATE;
    uint32_t byte_rate     = BYTES_PER_SEC;
    uint16_t block_align   = NUM_CHANNELS * (BITS_PER_SAMPLE / 8);
    uint16_t bits_per_samp = BITS_PER_SAMPLE;
    char     data_id[4]    = {'d','a','t','a'};
    uint32_t data_size     = 0;

    void set_data_size(uint32_t bytes)
    {
        data_size = bytes;
        file_size = bytes + sizeof(WavHeader) - 8;
    }
};

extern I2SClass i2s;

bool audio_begin();          // ES8311 codec (Wire must be started)
bool audio_i2s_begin();      // (re)starts I2S for recording and playback, waking the codec
void audio_sleep();          // stops I2S and powers the codec down until the next audio_i2s_begin()
void speaker_on();
void speaker_off();

// Non-blocking recorder (WAV, or MP3 with REC_MP3). A capture task buffers
// ~32 s of MP3 in PSRAM (~8 s without; WAV: ~16 s in PSRAM, ~3 s without);
// recorder_poll() must be called regularly to write it to the SD card. The
// WAV header is rewritten every 5 s so the file survives power loss.
bool recorder_start(fs::FS &fs, const char *path);
void recorder_poll();
void recorder_stop();                  // flushes the buffer and closes the file
bool recorder_active();
bool recorder_failed();                // SD write error or size limit; call recorder_stop()
float recorder_seconds();
float recorder_dropped_seconds();      // audio lost because the SD card was too slow

// Blocking wrapper: records until `duration_sec` elapses (0 = no limit)
// or `should_stop` returns true.
bool record_wav(fs::FS &fs, const char *path, int duration_sec,
                const std::function<bool()> &should_stop);

// Loud signal tones: rising two-note beep for start, falling three-note
// beep for stop. Blocking (~0.4 s).
enum Cue { CUE_START, CUE_STOP };
void play_cue(Cue cue);

void play_wav(fs::FS &fs, const char *path, const std::function<bool()> &should_stop = nullptr);
void play_test_tone(float freq_hz, int duration_ms);
