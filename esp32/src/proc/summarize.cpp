#include "pipeline.h"
#include "net/openrouter.h"
#include "store/config.h"
#include "store/recordings.h"
#include "store/templates.h"

#define MIN_TRANSCRIPT_CHARS  40      // below this there is nothing to summarize
#define ASK_CONTEXT_CHARS     300000  // transcript budget when asking across recordings

static const char SUMMARY_SYSTEM_PROMPT[] =
    "You turn transcripts from a pocket voice recorder into notes.\n"
    "Reply with a single JSON object and nothing else:\n"
    "{\"title\": \"short descriptive title, at most 8 words\",\n"
    " \"tags\": [\"2 to 5 short tags\"],\n"
    " \"summary\": \"the notes in Markdown, following the template instructions\",\n"
    " \"action_items\": [{\"task\": \"...\", \"owner\": \"name or empty\", \"due\": \"date or empty\"}],\n"
    " \"highlights\": [{\"time\": \"H:MM:SS\", \"note\": \"what was said and why it matters\"}]}\n"
    "Write everything in the language of the transcript. Use simple Markdown only "
    "(headings, bullet lists, bold); no tables. The transcript comes from speech "
    "recognition and may contain errors; fix obvious ones silently. The user pressed a "
    "button at the listed highlight times to mark important moments: explain each one "
    "in \"highlights\".";

static String summary_md(const JsonDocument &s)
{
    String md = "# " + String(s["title"] | "Untitled") + "\n\n";

    JsonArrayConst tags = s["tags"];
    if (tags.size()) {
        md += "_";
        for (size_t i = 0; i < tags.size(); i++) md += (i ? ", " : "") + String(tags[i] | "");
        md += "_\n\n";
    }
    md += String(s["summary"] | "") + "\n";

    JsonArrayConst actions = s["action_items"];
    if (actions.size()) {
        md += "\n## Action items\n\n";
        for (JsonObjectConst a : actions) {
            md += "- [ ] " + String(a["task"] | "");
            String owner = a["owner"] | "", due = a["due"] | "";
            if (!owner.isEmpty() || !due.isEmpty()) {
                md += " (" + owner;
                if (!owner.isEmpty() && !due.isEmpty()) md += ", ";
                md += due + ")";
            }
            md += "\n";
        }
    }

    JsonArrayConst highlights = s["highlights"];
    if (highlights.size()) {
        md += "\n## Highlights\n\n";
        for (JsonObjectConst h : highlights)
            md += "- **" + String(h["time"] | "") + "** " + String(h["note"] | "") + "\n";
    }
    return md;
}

Step summarize(const String &id, JsonDocument &meta)
{
    String transcript;
    if (!read_file(recording_path(id, "transcript.md"), transcript))
        return {STEP_FAILED, "transcript.md missing"};

    String name = meta["template"] | config_default_template();
    String instructions = template_load(name);
    if (instructions.isEmpty()) {
        name = "note";
        instructions = template_load(name);
    }

    JsonDocument summary;
    String model;
    if (transcript.length() < MIN_TRANSCRIPT_CHARS + 16) {  // "# Transcript" header only
        summary["title"] = "No speech";
        summary["summary"] = "No speech was detected in this recording.";
    } else {
        String system = SUMMARY_SYSTEM_PROMPT;
        system += "\n\nTemplate instructions:\n" + instructions;
        String glossary = glossary_load();
        if (!glossary.isEmpty())
            system += "\n\nCorrect spellings of names and terms that may occur:\n" + glossary;

        String user;
        uint32_t created = meta["created_unix"] | 0;
        if (created) user += "Recorded: " + recording_display_date(created) + "\n";
        user += "Duration: " + format_duration(meta["duration_s"] | 0.0f) + "\n";
        JsonArrayConst hl = meta["highlights"];
        if (hl.size()) {
            user += "Highlights: ";
            for (size_t i = 0; i < hl.size(); i++) user += (i ? ", " : "") + format_duration(hl[i] | 0.0f);
            user += "\n";
        }
        user += "\n" + transcript;
        transcript = "";  // free memory before the request

        String reply;
        ApiResult r = openrouter_chat(system, user, reply, &model);
        if (!r.ok) return {r.retryable() ? STEP_RETRY : STEP_FAILED, r.error, r.retry_after_s};

        if (!parse_json_reply(reply, summary) || !summary["summary"].is<const char *>()) {
            // Keep the answer even if the model ignored the JSON format
            summary.clear();
            summary["title"] = "Untitled";
            summary["summary"] = reply;
        }
    }

    String json;
    serializeJsonPretty(summary, json);
    if (!write_file(recording_path(id, "summary.json"), json) ||
        !write_file(recording_path(id, "summary.md"), summary_md(summary)))
        return {STEP_FAILED, "Cannot write summary"};

    meta["title"] = summary["title"] | "Untitled";
    meta["tags"] = summary["tags"];
    meta["template"] = name;
    meta["model"] = model;
    meta["state"] = "summarized";
    return {STEP_OK, ""};
}

// ============================================================
// Questions
// ============================================================

static String recording_context(const RecordingInfo &info, bool with_transcript)
{
    String ctx = "## " + recording_display_date(info.created_unix) + " - " +
                 recording_display_title(info) + " (" + format_duration(info.duration_s) + ")\n\n";
    String text;
    if (read_file(recording_path(info.id, "summary.md"), text)) ctx += text + "\n";
    else ctx += "(not summarized yet)\n";
    if (with_transcript && read_file(recording_path(info.id, "transcript.md"), text))
        ctx += "\n" + text + "\n";
    return ctx + "\n";
}

Step answer_question(const String &question_wav, const String &scope_id,
                     String &question, String &answer)
{
    File f = recordings_fs().open(question_wav, FILE_READ);
    if (!f) return {STEP_FAILED, "Question recording missing"};
    JsonDocument resp;
    ApiResult r = openrouter_transcribe(f.size(), [&](Print &out) {
        uint8_t buf[4096];
        size_t n;
        while ((n = f.read(buf, sizeof(buf))) > 0)
            if (out.write(buf, n) != n) return false;
        return true;
    }, false, resp);
    f.close();
    if (!r.ok) return {r.retryable() ? STEP_RETRY : STEP_FAILED, r.error, r.retry_after_s};

    question = resp["text"] | "";
    question.trim();
    if (question.isEmpty()) return {STEP_FAILED, "No question was heard."};

    String context;
    if (!scope_id.isEmpty()) {
        RecordingInfo info;
        if (!recording_info(scope_id, info)) return {STEP_FAILED, "Recording not found"};
        context = recording_context(info, true);
    } else {
        // Summaries of everything, full transcripts of the newest recordings
        size_t budget = ASK_CONTEXT_CHARS;
        for (const RecordingInfo &info : recordings_list()) {
            String ctx = recording_context(info, false);
            String transcript;
            if (budget > 0 && read_file(recording_path(info.id, "transcript.md"), transcript) &&
                transcript.length() < budget) {
                ctx += transcript + "\n\n";
                budget -= transcript.length();
            }
            context += ctx;
        }
        if (context.isEmpty()) context = "(no recordings yet)";
    }

    String system =
        "You answer questions about the user's recordings from a pocket voice recorder. "
        "The answer is shown on a small e-ink screen: be concise, use short paragraphs or "
        "bullet lists, no tables. Answer in the language of the question. Mention which "
        "recording (date and title) the information comes from. If the recordings do not "
        "contain the answer, say so.\n\nToday is " +
        recording_display_date(time(nullptr)) + ".\n\n# Recordings\n\n" + context;
    context = "";

    r = openrouter_chat(system, question, answer);
    if (!r.ok) return {r.retryable() ? STEP_RETRY : STEP_FAILED, r.error, r.retry_after_s};

    String log = "## " + recording_display_date(time(nullptr)) + "\n\n**Q:** " + question +
                 "\n\n**A:** " + answer + "\n\n";
    append_file(scope_id.isEmpty() ? String("/asks/log.md") : recording_path(scope_id, "qa.md"), log);
    return {STEP_OK, ""};
}
