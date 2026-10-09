#include "screens.h"
#include "audio/audio.h"
#include "proc/worker.h"
#include "session.h"
#include "store/recordings.h"

// ============================================================
// Recording in progress (buttons are handled globally in app.cpp)
// ============================================================

class RecordingScreen : public Screen {
public:
    void draw() override
    {
        SessionInfo info = session_info();
        GFXcanvas1 &g = gfx();

#if SCREEN_H >= 400
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
#else
        int cx = MARGIN + 6, cy = TITLE_Y - 5;
        if (pulse) g.fillCircle(cx, cy, 6, INK);
        else for (int r = 4; r <= 6; r++) g.drawCircle(cx, cy, r, INK);
        pulse = !pulse;
        draw_text(MARGIN + 18, TITLE_Y, "Recording", FONT_TITLE);

        int y = CONTENT_TOP + 38;
        draw_text_centered(y, format_duration(info.seconds).c_str(), FONT_DIGITS);
        if (!info.started.isEmpty())
            draw_text_centered(y + 18, ("since " + info.started).c_str(), FONT_SMALL);

        y = CONTENT_BOTTOM - 22;
        String hl = String(info.highlights) + (info.highlights == 1 ? " highlight" : " highlights");
        if (info.last_highlight >= 0) hl += ", last " + format_duration(info.last_highlight);
        draw_text_centered(y, hl.c_str(), FONT_BODY);

        if (info.dropped_seconds > 0) {
            char dropped[48];
            snprintf(dropped, sizeof(dropped), "%.1f s lost (slow SD card)", info.dropped_seconds);
            draw_text_centered(y + 17, dropped, FONT_BOLD);
        } else {
            char space[32];
            snprintf(space, sizeof(space), "SD card: %.0f h left", info.hours_left);
            draw_text_centered(y + 17, space, FONT_SMALL);
        }
#endif
        shown_s = (uint32_t)info.seconds;
    }

    void on_button(const ButtonEvent &) override {}
    void on_back() override {}

    // Redraw whenever the shown time changes, i.e. every second
    void tick() override
    {
        if ((uint32_t)recorder_seconds() != shown_s) ui_dirty();
    }

    // No periodic cleanup flashes while recording; a clean refresh follows when it stops
    bool auto_clean() override { return false; }
#ifdef HAS_ROCKER
    const char *hint() override { return "BOOT: stop · Rocker press: highlight"; }
#else
    const char *hint() override { return "BOOT: stop · PWR: highlight"; }
#endif

private:
    uint32_t shown_s = 0;
    bool pulse = true;
};

Screen *make_recording()
{
    return new RecordingScreen();
}
