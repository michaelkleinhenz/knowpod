#include "screens.h"
#include <ArduinoJson.h>
#include "audio/audio.h"
#include "proc/worker.h"
#include "store/config.h"
#include "store/recordings.h"

#if SCREEN_H < 400
#define COMPACT_DATES true   // no year: dates fit the 200 px screen
#else
#define COMPACT_DATES false
#endif

// ============================================================
// List
// ============================================================

class RecordingsScreen : public Screen {
public:
#ifdef HAS_ROCKER
    RecordingsScreen() { list.empty_text = "No recordings yet. Hold BOOT to record."; }
#else
    RecordingsScreen() { list.empty_text = "No recordings yet. Choose Record on the home screen."; }
#endif

    void draw() override
    {
        reload_if_changed();
        draw_title("Recordings");
        list.draw();
    }

    void on_button(const ButtonEvent &ev) override
    {
        if (list.on_button(ev)) {
            ui_dirty();
        } else if (ev.id == BTN_OK && ev.action == BTN_CLICK && !ids.empty()) {
            ui_push(make_detail(ids[list.selected]));
        }
    }

    void tick() override
    {
        if (recordings_generation() != generation && millis() - last_refresh > 10000) {
            last_refresh = millis();
            ui_dirty();
        }
    }

private:
    ListView list;
    std::vector<String> ids;
    uint32_t generation = 0;
    uint32_t last_refresh = 0;

    void reload_if_changed()
    {
        if (generation == recordings_generation()) return;
        generation = recordings_generation();
        String selected_id = ids.empty() ? String() : ids[list.selected];
        list.items.clear();
        ids.clear();
        for (const RecordingInfo &r : recordings_list()) {
            // The title is the date unless the recording has its own title
            String sub = r.title.isEmpty() ? String() : recording_display_date(r.created_unix, COMPACT_DATES) + " · ";
            sub += format_duration(r.duration_s) + " · " + recording_state_label(r);
            list.items.push_back({recording_display_title(r, COMPACT_DATES), sub});
            if (r.id == selected_id) list.selected = ids.size();
            ids.push_back(r.id);
        }
        list.selected = constrain(list.selected, 0, max(0, (int)ids.size() - 1));
    }
};

Screen *make_recordings()
{
    return new RecordingsScreen();
}

// ============================================================
// Detail
// ============================================================

class DetailScreen : public Screen {
public:
    explicit DetailScreen(const String &id) : id(id) {}

    void draw() override
    {
        if (reload || generation != recordings_generation()) load();
        String sub = recording_state_label(info);
        if (view.pages() > 1) sub += " · page " + String(view.page() + 1) + "/" + String(view.pages());
        draw_title(recording_display_title(info, COMPACT_DATES), sub);
        view.draw();
    }

    void on_button(const ButtonEvent &ev) override
    {
        if (view.on_button(ev)) ui_dirty();
        else if (ev.id == BTN_OK && ev.action == BTN_CLICK) open_actions();
    }

    void tick() override
    {
        // Follow the upload of this recording
        if (worker_current_id() == id && worker_progress() >= 0) {
            if (worker_progress() != shown_progress && millis() - last_refresh > 5000) {
                last_refresh = millis();
                reload = true;  // the upload line
                ui_dirty();
            }
        } else if (generation != recordings_generation() && millis() - last_refresh > 10000) {
            RecordingInfo now;
            if (recording_info(id, now) && (now.upload != info.upload || now.upload_percent != info.upload_percent)) {
                last_refresh = millis();
                ui_dirty();
            } else {
                generation = recordings_generation();
            }
        }
    }

#ifdef HAS_ROCKER
    const char *hint() override { return "Rocker: scroll · Press: menu · BOOT: back"; }
#else
    const char *hint() override { return "BOOT: scroll · PWR: menu"; }
#endif

private:
    String id;
    RecordingInfo info;
    TextView view;
    uint32_t generation = 0;
    uint32_t last_refresh = 0;
    int shown_progress = -1;
    bool reload = false;

    void load()
    {
        reload = false;
        generation = recordings_generation();
        int page = view.page();
        if (!recording_info(id, info)) {
            info.id = id;
            info.state = "missing";
        }

        JsonDocument meta;
        recording_load_meta(id, meta);
        view.clear();
        view.add("Recorded", FONT_SMALL);
        view.add(recording_display_date(info.created_unix));
        view.add("Duration", FONT_SMALL);
        view.add(format_duration(info.duration_s));
        if (!info.error.isEmpty()) {
            view.add("Error", FONT_SMALL);
            view.add(info.error);
        }
        JsonArrayConst hl = meta["highlights"];
        if (hl.size()) {
            view.add("Highlights", FONT_SMALL);
            String times;
            for (size_t i = 0; i < hl.size(); i++) times += (i ? ", " : "") + format_duration(hl[i] | 0.0f);
            view.add(times);
        }
        if ((meta["dropped_s"] | 0.0f) > 0) {
            view.add("Lost audio", FONT_SMALL);
            view.add(String(meta["dropped_s"] | 0.0f, 1) + " s (SD card too slow)");
        }
        if (meta["recovered"] | false) view.add("Recovered after a power loss.", FONT_SMALL);

        view.add("Upload", FONT_SMALL);
        if (info.upload == "done") view.add("Uploaded. Transcript and summary are in knowpod.");
        else if (info.upload == "failed") view.add("Failed: " + info.upload_error + "\n\nUse the menu to retry.");
        else if (!config_backend_enabled()) view.add("Add the backend token to config.json to upload.");
        else if (worker_current_id() == id) {
            shown_progress = worker_progress();
            view.add(worker_status());
        }
        else if (info.upload == "uploading") view.add("Uploading: " + String(info.upload_percent) + "%");
        else view.add("Waiting for Wi-Fi or other recordings.");

        view.add("Folder", FONT_SMALL);
        view.add(recording_dir(id));
        view.set_page(page);
    }

    void open_actions();
};

void DetailScreen::open_actions()
{
    // The device plays WAV only; MP3 recordings play in the browser (web page)
    std::vector<String> options;
    if (recording_audio_path(id).endsWith(".wav")) options.push_back("Play audio");
    if (info.upload == "failed") options.push_back("Retry upload");
    options.push_back("Delete");

    String rec_id = id;
    bool uploaded = info.upload == "done";
    ui_push(make_menu("Recording", options, 0, [=](int choice) {
        String option = options[choice];
        if (option == "Play audio") {
            show_progress("Playing", "Press any button to stop.");
            play_wav(recordings_fs(), recording_audio_path(rec_id).c_str(), [] {
                ButtonEvent ev;
                return buttons_get(ev);
            });
            ui_dirty();
        } else if (option == "Retry upload") {
            worker_retry(rec_id);
            ui_push(make_message("Queued", "The upload will be retried."));
        } else if (option == "Delete") {
            String body = uploaded ? "Delete this recording from the device? The uploaded copy stays in knowpod."
                                   : "Delete this recording? It has not been uploaded yet.";
            ui_push(make_confirm("Delete?", body, [=] {
                if (worker_current_id() == rec_id) {
                    ui_push(make_message("Busy", "This recording is being uploaded. Try again in a moment."));
                    return;
                }
                recording_delete(rec_id);
                ui_pop();  // the detail screen
            }));
        }
    }));
}

Screen *make_detail(const String &id)
{
    return new DetailScreen(id);
}
