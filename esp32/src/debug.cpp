#include "debug.h"
#include "app.h"
#include "store/sdcard.h"
#include "audio/audio.h"
#include "net/wifi.h"
#include "session.h"

// Serial console commands for testing audio without the UI.

#ifdef REC_MP3
#define TEST_RECORDING "/recording.mp3"
#else
#define TEST_RECORDING "/recording.wav"
#endif
#define STALL_REPORT_MS 5000

const char *volatile debug_stage = "setup";
static volatile uint32_t loop_count = 0;

extern TaskHandle_t loopTaskHandle;  // Arduino core (main.cpp)

static void print_menu();

// ============================================================
// List audio files on the SD card
// ============================================================

static void list_audio_files(const char *dir_path)
{
    File dir = SDCARD.open(dir_path);
    if (!dir || !dir.isDirectory()) {
        Serial.printf("Cannot open directory: %s\n", dir_path);
        return;
    }

    Serial.printf("Audio files in %s:\n", dir_path);
    int count = 0;
    File entry;
    while ((entry = dir.openNextFile())) {
        String name = entry.name();
        if (!entry.isDirectory() && (name.endsWith(".wav") || name.endsWith(".mp3"))) {
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
    Serial.println("  l - List audio files on SD card");
    Serial.println("  w - Connect Wi-Fi");
    Serial.println("========================================");
}

// ============================================================
// Stall detector
// ============================================================

static const char *task_state_name(eTaskState st)
{
    switch (st) {
    case eRunning:   return "running";
    case eReady:     return "ready (starved by other tasks)";
    case eBlocked:   return "blocked (waiting for a lock, queue or delay)";
    case eSuspended: return "suspended";
    default:         return "deleted";
    }
}

static void watch_task(void *)
{
    uint32_t seen = loop_count;
    uint32_t since = millis();
    for (;;) {
        delay(1000);
        if (loop_count != seen) {
            seen = loop_count;
            since = millis();
            continue;
        }
        uint32_t stuck = millis() - since;
        if (stuck < STALL_REPORT_MS || (stuck / 1000) % 5) continue;  // report every 5 s
        Serial.printf("STALL: main loop stuck for %lu s in \"%s\"; loop task %s; free heap %u\n",
                      (unsigned long)(stuck / 1000), (const char *)debug_stage,
                      loopTaskHandle ? task_state_name(eTaskGetState(loopTaskHandle)) : "?",
                      (unsigned)ESP.getFreeHeap());
    }
}

void debug_begin()
{
    print_menu();
    // Above the loop's priority so it still reports when the loop is starved
    xTaskCreatePinnedToCore(watch_task, "stall_watch", 3072, nullptr, 3, nullptr, 0);
}

void debug_loop()
{
    loop_count++;
    debug_stage = "debug console";
    if (!Serial.available()) return;

    char cmd = Serial.read();
    while (Serial.available()) Serial.read();  // drop the rest (e.g. newline)
    app_activity();  // a test recording needs the full clock for the MP3 encoder

    if (session_active() && strchr("rpt", tolower(cmd))) {
        Serial.println("A recording is in progress; stop it with BOOT first.");
        return;
    }

    switch (tolower(cmd)) {
    case 'r':
        Serial.println("Recording to " TEST_RECORDING " (send any key to stop) ...");
        record_wav(SDCARD, TEST_RECORDING, 0, [] {
            if (!Serial.available()) return false;
            while (Serial.available()) Serial.read();
            return true;
        });
        break;
    case 'p':
#ifdef REC_MP3
        Serial.println("This board records MP3, which it cannot play; download " TEST_RECORDING " instead.");
#else
        play_wav(SDCARD, TEST_RECORDING);
#endif
        break;
    case 't':
        play_test_tone(440.0f, 2000);
        break;
    case 'l':
        list_audio_files("/");
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
