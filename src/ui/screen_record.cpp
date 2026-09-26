#include "screens.h"
#include "audio/audio.h"
#include "proc/worker.h"
#include "session.h"
#include "store/recordings.h"

#define REC_REFRESH_MS       5000
#define QUESTION_MAX_S       60
#define QUESTION_PATH        "/asks/question.wav"

// ============================================================
// Recording in progress (buttons are handled globally in app.cpp)
// ============================================================

class RecordingScreen : public Screen {
public:
    void draw() override
    {
        SessionInfo info = session_info();

        // The dot alternates between filled and hollow with every update,
        // so a glance shows the recording is still running.
        GFXcanvas1 &g = gfx();
        int cx = MARGIN + 14, cy = TITLE_Y - 11;
        if (pulse) g.fillCircle(cx, cy, 13, INK);
        else for (int r = 10; r <= 13; r++) g.drawCircle(cx, cy, r, INK);
        pulse = !pulse;
        draw_text(MARGIN + 40, TITLE_Y, "Recording", FONT_TITLE);

        draw_text_centered(250, format_duration(info.seconds).c_str(), FONT_DIGITS);
        if (!info.started.isEmpty())
            draw_text_centered(300, ("since " + info.started).c_str(), FONT_BODY);

        int y = 400;
        String hl = String(info.highlights) + (info.highlights == 1 ? " highlight" : " highlights");
        if (info.last_highlight >= 0) hl += ", last at " + format_duration(info.last_highlight);
        draw_text(MARGIN, y, hl.c_str(), FONT_BODY);

        y += line_height(FONT_BODY) + 8;
        char space[48];
        snprintf(space, sizeof(space), "SD card: %.0f h left", info.hours_left);
        draw_text(MARGIN, y, space, FONT_BODY);

        if (info.dropped_seconds > 0) {
            y += line_height(FONT_BODY) + 8;
            char dropped[64];
            snprintf(dropped, sizeof(dropped), "Warning: %.1f s lost (slow SD card)", info.dropped_seconds);
            draw_text(MARGIN, y, dropped, FONT_BOLD);
        }
        last_draw = millis();
    }

    void on_button(const ButtonEvent &) override {}
    void on_back() override {}

    void tick() override
    {
        if (millis() - last_draw >= REC_REFRESH_MS) ui_dirty();
    }

    // No periodic cleanup flashes while recording; a clean refresh follows when it stops
    bool auto_clean() override { return false; }
    const char *hint() override { return "BOOT: highlight · Hold BOOT: stop"; }

private:
    uint32_t last_draw = 0;
    bool pulse = true;
};

Screen *make_recording()
{
    return new RecordingScreen();
}

// ============================================================
// Ask: record a spoken question, answer it from the recordings
// ============================================================

class AskScreen : public Screen {
public:
    explicit AskScreen(const String &scope_id) : scope(scope_id)
    {
        if (!scope.isEmpty()) {
            RecordingInfo info;
            if (recording_info(scope, info)) scope_title = recording_display_title(info);
        }
    }

    ~AskScreen() override
    {
        if (state == LISTENING) recorder_stop();
    }

    void draw() override
    {
        draw_title("Ask", scope.isEmpty() ? String("about all recordings") : "about " + scope_title);
        int y = CONTENT_TOP + 20;
        switch (state) {
        case READY:
            draw_paragraph(MARGIN, y, SCREEN_W - 2 * MARGIN,
                           "Press the rocker, ask your question, then press again.\n\n"
                           "Examples: \"What did we decide about the budget?\" - "
                           "\"What are my open tasks from this week?\"",
                           FONT_BODY, CONTENT_BOTTOM);
            break;
        case LISTENING:
            draw_text_centered(260, format_duration(recorder_seconds()).c_str(), FONT_DIGITS);
            draw_text_centered(310, "Listening ...", FONT_BODY);
            break;
        case THINKING:
            draw_paragraph(MARGIN, y, SCREEN_W - 2 * MARGIN,
                           "Thinking ...\n\nThe answer appears here in a moment.",
                           FONT_BODY, CONTENT_BOTTOM);
            break;
        }
        last_draw = millis();
    }

    void on_button(const ButtonEvent &ev) override
    {
        if (ev.id != BTN_OK || ev.action != BTN_CLICK) return;
        if (state == READY) start_listening();
        else if (state == LISTENING) finish_listening();
    }

    void on_back() override
    {
        if (state == LISTENING) {
            recorder_stop();
            state = READY;
            ui_dirty();
            return;
        }
        if (state == THINKING) return;  // the worker is busy; wait for the answer
        ui_pop();
    }

    void tick() override
    {
        if (state == LISTENING) {
            recorder_poll();
            if (recorder_failed() || recorder_seconds() >= QUESTION_MAX_S) finish_listening();
            else if (millis() - last_draw >= 5000) ui_dirty();
        } else if (state == THINKING) {
            String question, answer;
            bool ok;
            if (worker_ask_state(question, answer, ok) != ASK_DONE) return;
            worker_ask_reset();
            String body = ok ? "**" + question + "**\n\n" + answer
                             : (question.isEmpty() ? String() : "**" + question + "**\n\n") + answer;
            ui_replace(make_message(ok ? "Answer" : "Could not answer", body, true));
        }
    }

    const char *hint() override
    {
        switch (state) {
        case READY:     return "Press: start · Hold press: back";
        case LISTENING: return "Press: ask · BOOT: cancel";
        default:        return "";
        }
    }

private:
    enum State { READY, LISTENING, THINKING };
    State state = READY;
    String scope, scope_title;
    uint32_t last_draw = 0;

    void start_listening()
    {
        fs::FS &fs = recordings_fs();
        if (!fs.exists("/asks")) fs.mkdir("/asks");
        if (session_active() || recorder_active() || !recorder_start(fs, QUESTION_PATH)) {
            ui_push(make_message("Ask", "The microphone is busy."));
            return;
        }
        state = LISTENING;
        ui_dirty();
    }

    void finish_listening()
    {
        recorder_stop();
        if (recorder_seconds() < 1.0f) {
            state = READY;
            ui_dirty();
            return;
        }
        if (!worker_ask(QUESTION_PATH, scope)) {
            state = READY;
            ui_push(make_message("Ask", "Another question is still being answered."));
            return;
        }
        state = THINKING;
        ui_dirty();
    }
};

Screen *make_ask(const String &scope_id)
{
    return new AskScreen(scope_id);
}
