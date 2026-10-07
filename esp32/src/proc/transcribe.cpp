#include "pipeline.h"
#include <vector>
#include "audio/audio.h"
#include "net/openrouter.h"
#include "store/config.h"
#include "store/recordings.h"

#define SPLIT_SEARCH_S     30.0f   // look for a pause within +-30 s of the target
#define SPLIT_WINDOW_MS    400     // length of the quietest window
#define FRAME_MS           20
#define FRAME_SAMPLES      (SAMPLE_RATE * FRAME_MS / 1000)
#define PARAGRAPH_GAP_S    2.0f    // pause that starts a new paragraph
#define PARAGRAPH_MAX_S    60.0f

// ============================================================
// Chunking
// ============================================================

// Returns the center of the quietest SPLIT_WINDOW_MS window in [from, to].
static float find_quiet_point(File &f, float from, float to)
{
    const int window = SPLIT_WINDOW_MS / FRAME_MS;
    std::vector<float> energy;
    int16_t frame[FRAME_SAMPLES];

    f.seek(sizeof(WavHeader) + (uint32_t)(from * SAMPLE_RATE) * 2);
    int frames = (int)((to - from) * 1000 / FRAME_MS);
    for (int i = 0; i < frames; i++) {
        if (f.read((uint8_t *)frame, sizeof(frame)) != sizeof(frame)) break;
        int64_t sum = 0;
        for (int s = 0; s < FRAME_SAMPLES; s++) sum += (int32_t)frame[s] * frame[s];
        energy.push_back((float)sum / FRAME_SAMPLES);
    }
    if ((int)energy.size() <= window) return (from + to) / 2;

    float acc = 0;
    for (int i = 0; i < window; i++) acc += energy[i];
    float best = acc;
    int best_start = 0;
    for (int i = window; i < (int)energy.size(); i++) {
        acc += energy[i] - energy[i - window];
        if (acc < best) {
            best = acc;
            best_start = i - window + 1;
        }
    }
    return from + (best_start + window / 2) * FRAME_MS / 1000.0f;
}

Step transcribe_next_chunk(const String &id, JsonDocument &meta, String &progress)
{
    File f = recordings_fs().open(recording_audio_path(id), FILE_READ);
    if (!f) return {STEP_FAILED, "Audio file missing"};

    float file_s = (float)(f.size() > sizeof(WavHeader) ? f.size() - sizeof(WavHeader) : 0) / BYTES_PER_SEC;
    float duration = min(meta["duration_s"] | file_s, file_s);
    float chunk = config_stt_chunk_minutes() * 60.0f;

    // transcript.json holds the text so far and "until", the resume point;
    // both are written together so a crash never duplicates or loses a chunk.
    JsonDocument transcript;
    String json;
    float done = 0;
    if (read_file(recording_path(id, "transcript.json"), json) && !deserializeJson(transcript, json)) {
        done = transcript["until"] | 0.0f;
    } else {
        transcript.clear();
        transcript["segments"].to<JsonArray>();
    }

    float end = duration;
    if (duration - done > chunk * 1.25f)
        end = find_quiet_point(f, done + chunk - SPLIT_SEARCH_S, done + chunk + SPLIT_SEARCH_S);

    int total_chunks = max(1, (int)ceilf(duration / chunk));
    int chunk_no = min(total_chunks, (int)(done / chunk) + 1);
    progress = "chunk " + String(chunk_no) + "/" + String(total_chunks);

    if (end - done >= 0.5f) {
        uint32_t offset = sizeof(WavHeader) + (uint32_t)(done * SAMPLE_RATE) * 2;
        uint32_t bytes = (uint32_t)((end - done) * SAMPLE_RATE) * 2;

        auto write_chunk = [&](Print &out) {
            WavHeader hdr;
            hdr.set_data_size(bytes);
            out.write((const uint8_t *)&hdr, sizeof(hdr));
            f.seek(offset);
            uint8_t buf[4096];
            uint32_t left = bytes;
            while (left > 0) {
                size_t n = f.read(buf, min<uint32_t>(left, sizeof(buf)));
                if (n == 0 || out.write(buf, n) != n) return false;
                left -= n;
            }
            return true;
        };
        JsonDocument resp;
        ApiResult r = openrouter_transcribe(sizeof(WavHeader) + bytes, write_chunk, true, resp);
        if (!r.ok && r.status == 400) {
            // Some models reject timestamp output; fall back to plain text
            Serial.printf("Transcription with timestamps rejected (%s); retrying without\n", r.error.c_str());
            resp.clear();
            r = openrouter_transcribe(sizeof(WavHeader) + bytes, write_chunk, false, resp);
        }
        f.close();
        if (!r.ok) return {r.retryable() ? STEP_RETRY : STEP_FAILED, r.error, r.retry_after_s};

        JsonArray segments = transcript["segments"];
        JsonArrayConst got = resp["segments"];
        if (got.size() > 0) {
            for (JsonObjectConst s : got) {
                String text = s["text"] | "";
                text.trim();
                if (text.isEmpty()) continue;
                JsonObject seg = segments.add<JsonObject>();
                seg["start"] = roundf((done + (s["start"] | 0.0f)) * 10) / 10;
                seg["end"] = roundf((done + (s["end"] | 0.0f)) * 10) / 10;
                seg["text"] = text;
            }
        } else {
            String text = resp["text"] | "";
            text.trim();
            if (!text.isEmpty()) {
                JsonObject seg = segments.add<JsonObject>();
                seg["start"] = roundf(done * 10) / 10;
                seg["end"] = roundf(end * 10) / 10;
                seg["text"] = text;
            }
        }

    } else {
        f.close();
    }

    transcript["until"] = end;
    String out;
    serializeJson(transcript, out);
    if (!write_file(recording_path(id, "transcript.json"), out))
        return {STEP_FAILED, "Cannot write transcript.json"};

    meta["transcribed_s"] = end;  // informational (progress display)
    if (end >= duration - 0.01f) {
        write_transcript_md(id, meta);
        meta["state"] = "transcribed";
        meta.remove("transcribed_s");
    }
    return {STEP_OK, ""};
}

// ============================================================
// transcript.md
// ============================================================

struct Paragraph {
    float  start;
    String text;
};

static std::vector<Paragraph> paragraphs_of(const JsonDocument &transcript)
{
    std::vector<Paragraph> paras;
    float para_end = -1000;
    for (JsonObjectConst s : transcript["segments"].as<JsonArrayConst>()) {
        float start = s["start"] | 0.0f;
        const char *text = s["text"] | "";
        if (paras.empty() || start - para_end > PARAGRAPH_GAP_S ||
            start - paras.back().start > PARAGRAPH_MAX_S) {
            paras.push_back({start, text});
        } else {
            paras.back().text += " ";
            paras.back().text += text;
        }
        para_end = s["end"] | start;
    }
    return paras;
}

bool write_transcript_md(const String &id, const JsonDocument &meta)
{
    String json;
    JsonDocument transcript;
    if (!read_file(recording_path(id, "transcript.json"), json) || deserializeJson(transcript, json))
        return false;

    std::vector<Paragraph> paras = paragraphs_of(transcript);

    // Speaker per paragraph from the (optional) list of speaker changes
    std::vector<String> speakers(paras.size());
    for (JsonObjectConst c : transcript["speakers"].as<JsonArrayConst>()) {
        int from = c["paragraph"] | -1;
        const char *name = c["speaker"] | "";
        for (int i = max(from, 0); from >= 0 && i < (int)paras.size(); i++) speakers[i] = name;
    }

    std::vector<float> highlights;
    for (float h : meta["highlights"].as<JsonArrayConst>()) highlights.push_back(h);
    size_t next_hl = 0;

    String md = "# Transcript\n\n";
    if (paras.empty()) md += "_No speech detected._\n";
    String last_speaker;
    for (size_t i = 0; i < paras.size(); i++) {
        float para_end = i + 1 < paras.size() ? paras[i + 1].start : 1e9f;
        while (next_hl < highlights.size() && highlights[next_hl] < para_end) {
            md += "**Highlight " + format_duration(highlights[next_hl]) + "**\n\n";
            next_hl++;
        }
        md += "[" + format_duration(paras[i].start) + "] ";
        if (!speakers[i].isEmpty() && speakers[i] != last_speaker)
            md += "**" + speakers[i] + ":** ";
        last_speaker = speakers[i];
        md += paras[i].text + "\n\n";
    }
    return write_file(recording_path(id, "transcript.md"), md);
}

// ============================================================
// Speaker labels (experimental)
// ============================================================

Step label_speakers(const String &id, JsonDocument &meta)
{
    String json;
    JsonDocument transcript;
    if (!read_file(recording_path(id, "transcript.json"), json) || deserializeJson(transcript, json))
        return {STEP_FAILED, "transcript.json missing"};

    std::vector<Paragraph> paras = paragraphs_of(transcript);
    if (paras.size() < 2) {
        meta["speakers_done"] = true;
        return {STEP_OK, ""};
    }

    String user;
    for (size_t i = 0; i < paras.size(); i++)
        user += "[" + String(i) + "] " + paras[i].text + "\n";

    const char *system =
        "You get a numbered transcript of a recording, one paragraph per line. "
        "Work out who is speaking in each paragraph from content and context. "
        "Use real names when they are mentioned or clearly implied, otherwise "
        "'Speaker 1', 'Speaker 2', ... If there is only one speaker, return an empty list.\n"
        "Reply with JSON only: {\"speakers\": [{\"paragraph\": <number>, \"speaker\": \"<name>\"}]} "
        "listing only the paragraphs where the speaker changes (always include paragraph 0 "
        "if there are several speakers).";

    String reply;
    ApiResult r = openrouter_chat(system, user, reply);
    if (!r.ok) {
        if (r.retryable()) return {STEP_RETRY, r.error};
        Serial.printf("Speaker labels skipped: %s\n", r.error.c_str());
        meta["speakers_done"] = true;  // optional step; don't block the summary
        return {STEP_OK, ""};
    }

    JsonDocument parsed;
    if (parse_json_reply(reply, parsed) && parsed["speakers"].is<JsonArray>()) {
        transcript["speakers"] = parsed["speakers"];
        String out;
        serializeJson(transcript, out);
        write_file(recording_path(id, "transcript.json"), out);
        write_transcript_md(id, meta);
    } else {
        Serial.println("Speaker labels: unparseable reply, skipped");
    }
    meta["speakers_done"] = true;
    return {STEP_OK, ""};
}

bool parse_json_reply(const String &reply, JsonDocument &out)
{
    int start = reply.indexOf('{');
    int end = reply.lastIndexOf('}');
    if (start < 0 || end <= start) return false;
    return !deserializeJson(out, reply.substring(start, end + 1));
}
