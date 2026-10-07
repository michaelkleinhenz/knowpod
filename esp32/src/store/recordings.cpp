#include "recordings.h"
#include <SD_MMC.h>
#include <algorithm>
#include <atomic>
#include "audio/audio.h"
#include "hw/clock.h"

static fs::FS *rec_fs = nullptr;
static SemaphoreHandle_t mutex = xSemaphoreCreateRecursiveMutex();
static std::atomic<uint32_t> generation{1};
static uint32_t cached_generation = 0;
static std::vector<RecordingInfo> cache;

struct Lock {
    Lock()  { xSemaphoreTakeRecursive(mutex, portMAX_DELAY); }
    ~Lock() { xSemaphoreGiveRecursive(mutex); }
};

fs::FS &recordings_fs()                          { return *rec_fs; }
String recording_dir(const String &id)           { return String(RECS_DIR "/") + id; }
String recording_path(const String &id, const char *file) { return recording_dir(id) + "/" + file; }
String recording_audio_path(const String &id)    { return recording_path(id, "audio.wav"); }
uint32_t recordings_generation()                 { return generation; }
void recordings_touch()                          { generation++; }

bool recording_valid_id(const String &id)
{
    if (id.isEmpty() || id.length() > 64) return false;
    for (char c : id)
        if (!isalnum((unsigned char)c) && c != '-' && c != '_') return false;
    return true;
}

// ============================================================
// Files
// ============================================================

bool read_file(const String &path, String &out)
{
    File f = rec_fs->open(path, FILE_READ);
    if (!f) return false;
    out = "";
    out.reserve(f.size());
    uint8_t buf[512];
    size_t n;
    while ((n = f.read(buf, sizeof(buf))) > 0) out.concat((const char *)buf, n);
    f.close();
    return true;
}

bool write_file(const String &path, const String &content)
{
    // Write a temp file first so a power cut never leaves a truncated file
    String tmp = path + ".tmp";
    File f = rec_fs->open(tmp, FILE_WRITE);
    if (!f) return false;
    bool ok = f.write((const uint8_t *)content.c_str(), content.length()) == content.length();
    f.close();
    if (!ok) return false;
    rec_fs->remove(path);
    return rec_fs->rename(tmp, path);
}

bool append_file(const String &path, const String &content)
{
    File f = rec_fs->open(path, FILE_APPEND);
    if (!f) return false;
    bool ok = f.write((const uint8_t *)content.c_str(), content.length()) == content.length();
    f.close();
    return ok;
}

// ============================================================
// Recordings
// ============================================================

String recording_new_id()
{
    Lock lock;
    String base = clock_valid() ? clock_format("%Y%m%d-%H%M%S") : String("rec");
    if (clock_valid() && !rec_fs->exists(recording_dir(base))) return base;

    for (int i = 1; i < 10000; i++) {
        char suffix[8];
        snprintf(suffix, sizeof(suffix), "-%04d", i);
        String id = base + suffix;
        if (!rec_fs->exists(recording_dir(id))) return id;
    }
    return base + "-" + String(millis());
}

bool recording_create(const String &id)
{
    bool ok = rec_fs->mkdir(recording_dir(id));
    recordings_touch();
    return ok;
}

bool recording_delete(const String &id)
{
    if (!recording_valid_id(id)) return false;
    Lock lock;
    String dir_path = recording_dir(id);
    File dir = rec_fs->open(dir_path);
    if (!dir) return false;
    std::vector<String> files;
    File entry;
    while ((entry = dir.openNextFile())) {
        files.push_back(dir_path + "/" + entry.name());
        entry.close();
    }
    dir.close();
    for (const String &f : files) rec_fs->remove(f);
    bool ok = rec_fs->rmdir(dir_path);
    recordings_touch();
    Serial.printf("Deleted recording %s\n", id.c_str());
    return ok;
}

bool recording_load_meta(const String &id, JsonDocument &meta)
{
    File f = rec_fs->open(recording_path(id, "meta.json"), FILE_READ);
    if (!f) return false;
    DeserializationError err = deserializeJson(meta, f);
    f.close();
    return !err;
}

bool recording_save_meta(const String &id, const JsonDocument &meta)
{
    String json;
    serializeJsonPretty(meta, json);
    bool ok;
    {
        Lock lock;
        ok = write_file(recording_path(id, "meta.json"), json);
    }
    recordings_touch();
    return ok;
}

static RecordingInfo info_from_meta(const String &id, const JsonDocument &meta)
{
    RecordingInfo info;
    info.id = id;
    info.title = meta["title"] | "";
    info.created_unix = meta["created_unix"] | 0;
    info.duration_s = meta["duration_s"] | 0.0f;
    info.highlights = meta["highlights"].size();
    info.state = meta["state"] | "recorded";
    info.error = meta["error"] | "";
    info.upload = meta["upload"]["status"] | "";
    info.upload_error = meta["upload"]["error"] | "";
    size_t size = meta["upload"]["size"] | 0;
    info.upload_percent = info.upload == "done" ? 100 : size ? (int)(100.0 * (meta["upload"]["offset"] | 0L) / size) : 0;
    info.highlights_unsynced = info.upload == "done" && info.highlights > 0 &&
                               !(meta["upload"]["highlights_synced"] | false);
    return info;
}

bool recording_info(const String &id, RecordingInfo &info)
{
    JsonDocument meta;
    if (!recording_load_meta(id, meta)) return false;
    info = info_from_meta(id, meta);
    return true;
}

std::vector<RecordingInfo> recordings_list()
{
    Lock lock;
    if (cached_generation == generation) return cache;
    cached_generation = generation;
    cache.clear();

    File dir = rec_fs->open(RECS_DIR);
    File entry;
    while (dir && (entry = dir.openNextFile())) {
        bool is_dir = entry.isDirectory();
        String id = entry.name();
        entry.close();
        if (!is_dir) continue;
        RecordingInfo info;
        if (recording_info(id, info)) cache.push_back(info);
    }
    if (dir) dir.close();

    std::sort(cache.begin(), cache.end(), [](const RecordingInfo &a, const RecordingInfo &b) {
        if (a.created_unix != b.created_unix) return a.created_unix > b.created_unix;
        return a.id > b.id;
    });
    return cache;
}

// ============================================================
// Display helpers
// ============================================================

String format_duration(float seconds)
{
    uint32_t s = seconds > 0 ? (uint32_t)seconds : 0;
    char buf[16];
    snprintf(buf, sizeof(buf), "%u:%02u:%02u", s / 3600, (s / 60) % 60, s % 60);
    return buf;
}

String recording_display_date(uint32_t unix_time)
{
    if (!unix_time) return "Unknown date";
    time_t t = unix_time;
    tm local;
    localtime_r(&t, &local);
    char buf[32];
    strftime(buf, sizeof(buf), "%a %d.%m.%Y %H:%M", &local);
    return buf;
}

String recording_display_title(const RecordingInfo &info)
{
    if (!info.title.isEmpty()) return info.title;
    return info.created_unix ? recording_display_date(info.created_unix) : info.id;
}

String recording_state_label(const String &state)
{
    if (state == "recording")    return "Recording";
    if (state == "recorded")     return "Waiting";
    if (state == "transcribing") return "Transcribing";
    if (state == "transcribed")  return "Summarizing";
    if (state == "summarized")   return "Done";
    if (state == "error")        return "Error";
    return state;
}

// ============================================================
// Startup
// ============================================================

// A recording still marked "recording" at boot was cut off by a power loss or
// crash. The WAV header is checkpointed every 5 s, but the file length (flushed
// at the same time) is the better measure, so the header is rebuilt from it.
static void recover(const String &id, JsonDocument &meta)
{
    File f = rec_fs->open(recording_audio_path(id), "r+");
    uint32_t data_bytes = 0;
    if (f) {
        if (f.size() > sizeof(WavHeader))
            data_bytes = (f.size() - sizeof(WavHeader)) & ~1u;  // whole samples
        WavHeader hdr;
        hdr.set_data_size(data_bytes);
        f.seek(0);
        f.write((const uint8_t *)&hdr, sizeof(hdr));
        f.close();
    }

    meta["duration_s"] = (float)data_bytes / BYTES_PER_SEC;
    meta["state"] = "recorded";
    meta["recovered"] = true;
    recording_save_meta(id, meta);
    Serial.printf("Recovered interrupted recording %s (%.0f s)\n", id.c_str(),
                  (float)data_bytes / BYTES_PER_SEC);
}

bool recordings_begin(fs::FS &fs)
{
    rec_fs = &fs;
    if (!fs.exists(RECS_DIR) && !fs.mkdir(RECS_DIR)) {
        Serial.println("Failed to create " RECS_DIR);
        return false;
    }

    File dir = fs.open(RECS_DIR);
    File entry;
    std::vector<String> ids;
    while ((entry = dir.openNextFile())) {
        if (entry.isDirectory()) ids.push_back(entry.name());
        entry.close();
    }
    dir.close();

    for (const String &id : ids) {
        JsonDocument meta;
        if (recording_load_meta(id, meta) && meta["state"] == "recording")
            recover(id, meta);
    }

    // The first free-space query scans the FAT and can take seconds on large
    // cards; do it now so starting a recording later is instant.
    Serial.printf("%u recordings on SD card, %.1f GB free\n", (unsigned)ids.size(),
                  (SD_MMC.totalBytes() - SD_MMC.usedBytes()) / 1e9);
    return true;
}
