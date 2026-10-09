#include "screens.h"
#include "app.h"
#include "net/web.h"
#include "net/wifi.h"
#include "proc/worker.h"
#include "session.h"
#include "store/config.h"
#include "store/recordings.h"

#define UPLOAD_REFRESH_MS  5000   // e-ink refresh interval while an upload is running

static void draw_progress_bar(int x, int y, int w, int h, int percent)
{
    GFXcanvas1 &g = gfx();
    g.drawRect(x, y, w, h, INK);
    int fill = (w - 4) * constrain(percent, 0, 100) / 100;
    if (fill > 0) g.fillRect(x + 2, y + 2, fill, h - 4, INK);
}

// ============================================================
// Message
// ============================================================

class MessageScreen : public Screen {
public:
    MessageScreen(const String &title, const String &body, bool markdown, uint32_t home_after_ms)
        : title(title), home_after_ms(home_after_ms)
    {
#ifdef HAS_ROCKER
        if (home_after_ms) home_hint = "Press: close · Home in " + String(home_after_ms / 1000) + " s";
#else
        if (home_after_ms) home_hint = "PWR: close · home in " + String(home_after_ms / 1000) + " s";
#endif
        if (markdown) view.add_markdown(body);
        else {
            int pos = 0;
            while (pos <= (int)body.length()) {
                int nl = body.indexOf('\n', pos);
                view.add(body.substring(pos, nl < 0 ? body.length() : nl));
                pos = nl < 0 ? body.length() + 1 : nl + 1;
            }
        }
    }

    void draw() override
    {
        int pages = view.pages();
        draw_title(title, pages > 1 ? "Page " + String(view.page() + 1) + " of " + String(pages) : String());
        view.draw();
        if (!shown_at) shown_at = millis();
    }

    void on_button(const ButtonEvent &ev) override
    {
        if (view.on_button(ev)) {
            ui_dirty();
            shown_at = millis();  // reading: restart the timer
        } else if (ev.id == BTN_OK && ev.action == BTN_CLICK) {
            ui_pop();
        }
    }

    void tick() override
    {
        if (home_after_ms && shown_at && millis() - shown_at >= home_after_ms) {
            home_after_ms = 0;
            ui_pop_to_root();
        }
    }

    const char *hint() override
    {
        if (home_after_ms) return home_hint.c_str();
#ifdef HAS_ROCKER
        return view.pages() > 1 ? "Rocker: scroll · Press: close" : "Press: close";
#else
        return view.pages() > 1 ? "BOOT: scroll · PWR: close" : "PWR: close";
#endif
    }

private:
    String title;
    TextView view;
    uint32_t home_after_ms;
    uint32_t shown_at = 0;
    String home_hint;
};

Screen *make_message(const String &title, const String &body, bool markdown, uint32_t home_after_ms)
{
    return new MessageScreen(title, body, markdown, home_after_ms);
}

void show_progress(const String &title, const String &body)
{
    ui_push(make_message(title, body));
    ui_render(true);
    ui_pop();
}

// ============================================================
// Menu
// ============================================================

class MenuScreen : public Screen {
public:
    MenuScreen(const String &title, const std::vector<String> &options, int selected,
               std::function<void(int)> on_select)
        : title(title), on_select(on_select)
    {
        for (const String &o : options) list.items.push_back({o, ""});
        list.selected = constrain(selected, 0, (int)options.size() - 1);
    }

    void draw() override
    {
        draw_title(title);
        list.draw();
    }

    void on_button(const ButtonEvent &ev) override
    {
        if (list.on_button(ev)) {
            ui_dirty();
        } else if (ev.id == BTN_OK && ev.action == BTN_CLICK && !list.items.empty()) {
            auto cb = on_select;
            int sel = list.selected;
            ui_pop();  // deferred delete: `this` stays valid until the next loop
            cb(sel);
        }
    }

#ifdef HAS_ROCKER
    const char *hint() override { return "Press: choose · Hold press: back"; }
#else
    const char *hint() override { return "BOOT: next · PWR: pick"; }
#endif

private:
    String title;
    ListView list;
    std::function<void(int)> on_select;
};

Screen *make_menu(const String &title, const std::vector<String> &options, int selected,
                  std::function<void(int)> on_select)
{
    return new MenuScreen(title, options, selected, on_select);
}

// ============================================================
// Confirm
// ============================================================

class ConfirmScreen : public Screen {
public:
    ConfirmScreen(const String &title, const String &body, std::function<void()> on_confirm)
        : title(title), body(body), on_confirm(on_confirm) {}

    void draw() override
    {
        draw_title(title);
        draw_paragraph(MARGIN, CONTENT_TOP + 10, SCREEN_W - 2 * MARGIN, body, FONT_BODY, CONTENT_BOTTOM);
    }

    void on_button(const ButtonEvent &ev) override
    {
        if (ev.id == BTN_OK && ev.action == BTN_CLICK) {
            auto cb = on_confirm;
            ui_pop();
            cb();
        }
    }

#ifdef HAS_ROCKER
    const char *hint() override { return "Press: confirm · BOOT: cancel"; }
#else
    const char *hint() override { return "PWR: yes · Hold BOOT: no"; }
#endif

private:
    String title, body;
    std::function<void()> on_confirm;
};

Screen *make_confirm(const String &title, const String &body, std::function<void()> on_confirm)
{
    return new ConfirmScreen(title, body, on_confirm);
}

// ============================================================
// Home
// ============================================================

class HomeScreen : public Screen {
public:
    HomeScreen()
    {
        for (const char *item : {"Record", "Recordings", "Settings"}) list.items.push_back({item, ""});
#if SCREEN_H >= 400
        list.top = 150;
        list.bottom = 150 + 3 * 66;
#else
        list.top = CONTENT_TOP;
        list.bottom = CONTENT_TOP + 3 * 26;
#endif
    }

    void draw() override
    {
#if SCREEN_H >= 400
        draw_logo(SCREEN_W / 2, TITLE_Y - 5);
#else
        draw_logo(SCREEN_W / 2, TITLE_Y - 4, 20);
#endif
        list.draw();

#if SCREEN_H >= 400
        int y = list.bottom + 30;
        std::vector<RecordingInfo> recs = recordings_list();
        String info = String(recs.size()) + (recs.size() == 1 ? " recording" : " recordings");
        int uploads = worker_pending_uploads();
        if (uploads) info += " · " + String(uploads) + " to upload";
        y = draw_paragraph(MARGIN, y, SCREEN_W - 2 * MARGIN, info, FONT_BODY, CONTENT_BOTTOM);

        String status = worker_status();
        if (!status.isEmpty())
            y = draw_paragraph(MARGIN, y + 6, SCREEN_W - 2 * MARGIN, status, FONT_SMALL, CONTENT_BOTTOM);
        if (worker_progress() >= 0) draw_progress_bar(MARGIN, y + 8, SCREEN_W - 2 * MARGIN, 14, worker_progress());

        int ny = CONTENT_BOTTOM - 2 * line_height(FONT_SMALL) - 8;
        gfx().fillRect(MARGIN, ny - 10, SCREEN_W - 2 * MARGIN, 1, INK);
        int lh = line_height(FONT_SMALL);
        int asc = font_ascent(FONT_SMALL);
        if (web_active()) {
            int x = draw_text(MARGIN, ny + asc, "IP  ", FONT_BOLD);
            draw_text(x, ny + asc, wifi_ip().c_str(), FONT_SMALL);
            x = draw_text(MARGIN, ny + lh + asc, "Web  ", FONT_BOLD);
            draw_text(x, ny + lh + asc, web_url().c_str(), FONT_SMALL);
        } else {
            String net = wifi_connected() ? "IP " + wifi_ip() : String("Wi-Fi off");
            draw_text(MARGIN, ny + asc, net.c_str(), FONT_SMALL);
            draw_paragraph(MARGIN, ny + lh, SCREEN_W - 2 * MARGIN,
                           config_wifi().empty() ? "Add Wi-Fi in /config.json to upload recordings."
                                                 : "Web access: Settings", FONT_SMALL, CONTENT_BOTTOM);
        }
#else
        std::vector<RecordingInfo> recs = recordings_list();
        String info = String(recs.size()) + (recs.size() == 1 ? " recording" : " recordings");
        int uploads = worker_pending_uploads();
        if (uploads) info += " · " + String(uploads) + " to upload";
        int percent = worker_progress();
        if (percent >= 0) {
            info = "Uploading " + String(percent) + "%";
            if (uploads > 1) info += " · " + String(uploads - 1) + " more";
            draw_progress_bar(MARGIN, CONTENT_BOTTOM - 26, SCREEN_W - 2 * MARGIN, 8, percent);
        }
        draw_text_centered(CONTENT_BOTTOM - 4, info.c_str(), FONT_SMALL);
#endif
        seen_generation = worker_generation();
        seen_web = web_active();
    }

    void on_button(const ButtonEvent &ev) override
    {
        if (list.on_button(ev)) {
            ui_dirty();
            return;
        }
        if (ev.id != BTN_OK || ev.action != BTN_CLICK) return;
        switch (list.selected) {
        case 0: start_recording_from_ui(); break;
        case 1: ui_push(make_recordings()); break;
        case 2: ui_push(make_settings()); break;
        }
    }

    void tick() override
    {
        // Show upload progress, but don't refresh the e-ink too often
        uint32_t interval = worker_progress() >= 0 ? UPLOAD_REFRESH_MS : 15000;
        if (web_active() != seen_web) ui_dirty();  // URLs appear/disappear
        else if (worker_generation() != seen_generation && millis() - last_refresh > interval) {
            last_refresh = millis();
            ui_dirty();
        }
    }

    void on_back() override {}
#ifdef HAS_ROCKER
    const char *hint() override { return "Hold BOOT to record"; }
#else
    const char *hint() override { return "PWR: select · Hold BOOT: sleep"; }
#endif

private:
    ListView list;
    uint32_t seen_generation = 0;
    uint32_t last_refresh = 0;
    bool seen_web = false;
};

Screen *make_home()
{
    return new HomeScreen();
}

// ============================================================
// Sleep
// ============================================================

class SleepScreen : public Screen {
public:
    void draw() override
    {
#if SCREEN_H >= 400
        draw_logo(SCREEN_W / 2, SCREEN_H / 2 - 50, 72);
        draw_text_centered(SCREEN_H / 2 + 20, "Sleeping", FONT_BODY);
        int pending = worker_pending_uploads();
        if (pending)
            draw_text_centered(SCREEN_H / 2 + 50,
                               (String(pending) + " recording(s) waiting for upload").c_str(), FONT_SMALL);
#else
        draw_logo(SCREEN_W / 2, SCREEN_H / 2 - 20, 22);
        draw_text_centered(SCREEN_H / 2 + 14, "Sleeping", FONT_SMALL);
#endif
    }
    void on_button(const ButtonEvent &) override {}
#ifdef HAS_ROCKER
    const char *hint() override { return "Press PWR or rocker to wake"; }
#else
    const char *hint() override { return "Press PWR to wake"; }
#endif
};

Screen *make_sleep()
{
    return new SleepScreen();
}
