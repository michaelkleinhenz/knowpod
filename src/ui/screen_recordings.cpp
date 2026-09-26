#include "screens.h"
#include <ArduinoJson.h>
#include "audio/audio.h"
#include "proc/worker.h"
#include "store/config.h"
#include "store/recordings.h"
#include "store/templates.h"

// ============================================================
// List
// ============================================================

class RecordingsScreen : public Screen {
public:
    RecordingsScreen() { list.empty_text = "No recordings yet. Hold BOOT to record."; }

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
            String sub = r.created_unix ? recording_display_date(r.created_unix) : r.id;
            sub += " · " + format_duration(r.duration_s);
            if (r.state != "summarized") sub += " · " + recording_state_label(r.state);
            if (r.upload == "failed") sub += " · upload failed";
            list.items.push_back({recording_display_title(r), sub});
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

enum DetailView { VIEW_SUMMARY, VIEW_ACTIONS, VIEW_TRANSCRIPT, VIEW_DETAILS };

static const char *const VIEW_NAMES[] = {"Summary", "Action items", "Transcript", "Details"};

class DetailScreen : public Screen {
public:
    explicit DetailScreen(const String &id) : id(id) {}

    void draw() override
    {
        if (generation != recordings_generation()) load();
        String sub = VIEW_NAMES[view_mode];
        if (view.pages() > 1) sub += " · page " + String(view.page() + 1) + "/" + String(view.pages());
        draw_title(recording_display_title(info), sub);
        view.draw();
    }

    void on_button(const ButtonEvent &ev) override
    {
        if (view.on_button(ev)) ui_dirty();
        else if (ev.id == BTN_OK && ev.action == BTN_CLICK) open_actions();
    }

    void tick() override
    {
        // Follow processing progress of this recording
        if (generation != recordings_generation() && millis() - last_refresh > 10000) {
            RecordingInfo now;
            if (recording_info(id, now) && now.state != info.state) {
                last_refresh = millis();
                ui_dirty();
            } else {
                generation = recordings_generation();
            }
        }
    }

    const char *hint() override { return "Rocker: scroll · Press: menu · BOOT: back"; }

    void show(DetailView v)
    {
        view_mode = v;
        view.clear();  // start at the first page
        load();
        ui_dirty();
    }

private:
    String id;
    RecordingInfo info;
    DetailView view_mode = VIEW_SUMMARY;
    bool first_load = true;
    TextView view;
    uint32_t generation = 0;
    uint32_t last_refresh = 0;

    String processing_note()
    {
        if (info.state == "error") return "Processing failed: " + info.error + "\n\nUse the menu to retry.";
        if (info.state == "summarized") return "";
        if (config_processing_backend()) {
            if (info.upload == "failed") return "The upload failed: " + info.upload_error + "\n\nUse the menu to retry.";
            if (info.upload != "done")
                return "Waiting for the upload (" + String(info.upload_percent) + " %). The backend "
                       "transcribes and summarizes it afterwards.";
            String note = "Uploaded. Waiting for the backend's transcript and summary.";
            if (worker_current_id() == id) note += "\n\n" + worker_status();
            return note;
        }
        String note = "Not summarized yet (" + recording_state_label(info.state) + ").";
        if (worker_current_id() == id) note += "\n\n" + worker_status();
        else if (worker_pending()) note += "\n\nWaiting for Wi-Fi or other recordings.";
        return note;
    }

    void load()
    {
        generation = recordings_generation();
        int page = view.page();
        String old_state = info.state;
        if (!recording_info(id, info)) {
            info.id = id;
            info.state = "missing";
        }
        if (first_load) {
            view_mode = info.state == "summarized" ? VIEW_SUMMARY : VIEW_DETAILS;
            first_load = false;
        }

        view.clear();
        String content;
        JsonDocument summary;
        switch (view_mode) {
        case VIEW_SUMMARY:
            if (read_file(recording_path(id, "summary.json"), content) && !deserializeJson(summary, content)) {
                JsonArrayConst tags = summary["tags"];
                if (tags.size()) {
                    String t;
                    for (size_t i = 0; i < tags.size(); i++) t += (i ? ", " : "") + String(tags[i] | "");
                    view.add(t, FONT_SMALL);
                    view.add("");
                }
                view.add_markdown(summary["summary"] | "");
            } else {
                view.add(processing_note());
            }
            break;

        case VIEW_ACTIONS:
            if (read_file(recording_path(id, "summary.json"), content) && !deserializeJson(summary, content)) {
                JsonArrayConst actions = summary["action_items"];
                view.add("Action items", FONT_BOLD);
                if (!actions.size()) view.add("None.");
                for (JsonObjectConst a : actions) {
                    String line = "[ ] " + String(a["task"] | "");
                    String owner = a["owner"] | "", due = a["due"] | "";
                    if (!owner.isEmpty()) line += " - " + owner;
                    if (!due.isEmpty()) line += " (" + due + ")";
                    view.add(line);
                }
                JsonArrayConst hl = summary["highlights"];
                if (hl.size()) {
                    view.add("");
                    view.add("Highlights", FONT_BOLD);
                    for (JsonObjectConst h : hl)
                        view.add(String(h["time"] | "") + "  " + String(h["note"] | ""));
                }
            } else {
                view.add(processing_note());
            }
            break;

        case VIEW_TRANSCRIPT:
            if (read_file(recording_path(id, "transcript.md"), content)) {
                if (content.startsWith("# Transcript")) content.remove(0, 12);
                view.add_markdown(content);
            } else {
                view.add(processing_note());
            }
            break;

        case VIEW_DETAILS: {
            JsonDocument meta;
            recording_load_meta(id, meta);
            view.add("Recorded", FONT_SMALL);
            view.add(recording_display_date(info.created_unix));
            view.add("Duration", FONT_SMALL);
            view.add(format_duration(info.duration_s));
            view.add("Status", FONT_SMALL);
            view.add(recording_state_label(info.state) + (info.error.isEmpty() ? "" : ": " + info.error));
            if (worker_current_id() == id) view.add(worker_status());
            JsonArrayConst hl = meta["highlights"];
            if (hl.size()) {
                view.add("Highlights", FONT_SMALL);
                String times;
                for (size_t i = 0; i < hl.size(); i++) times += (i ? ", " : "") + format_duration(hl[i] | 0.0f);
                view.add(times);
            }
            if (meta["template"].is<const char *>()) {
                view.add("Template", FONT_SMALL);
                view.add(meta["template"] | "");
            }
            if (meta["model"].is<const char *>()) {
                view.add("Model", FONT_SMALL);
                view.add(meta["model"] | "");
            }
            if ((meta["dropped_s"] | 0.0f) > 0) {
                view.add("Lost audio", FONT_SMALL);
                view.add(String(meta["dropped_s"] | 0.0f, 1) + " s (SD card too slow)");
            }
            if (meta["recovered"] | false) view.add("Recovered after a power loss.", FONT_SMALL);
            if (config_backend_enabled() || !info.upload.isEmpty()) {
                view.add("Backend upload", FONT_SMALL);
                if (info.upload == "done") view.add("Uploaded");
                else if (info.upload == "failed") view.add("Failed: " + info.upload_error);
                else if (info.upload == "uploading") view.add(String(info.upload_percent) + " % uploaded");
                else view.add("Waiting");
            }
            view.add("Folder", FONT_SMALL);
            view.add(recording_dir(id));
            break;
        }
        }
        // Stay on the same page when only the state changed
        if (old_state == info.state) view.set_page(page);
    }

    void open_actions();
};

void DetailScreen::open_actions()
{
    std::vector<String> options = {"Summary", "Action items & highlights", "Transcript", "Details",
                                   "Ask about this recording", "Play audio"};
    if (!config_processing_backend()) options.push_back("Summarize with template...");
    if (info.state == "error") options.push_back("Retry processing");
    if (info.upload == "failed") options.push_back("Retry upload");
    options.push_back("Delete");

    String rec_id = id;
    DetailScreen *self = this;
    ui_push(make_menu("Recording", options, view_mode, [=](int choice) {
        String option = options[choice];
        if (choice <= VIEW_DETAILS) {
            self->show((DetailView)choice);
        } else if (option.startsWith("Ask")) {
            ui_push(make_ask(rec_id));
        } else if (option == "Play audio") {
            show_progress("Playing", "Press any button to stop.");
            play_wav(recordings_fs(), recording_audio_path(rec_id).c_str(), [] {
                ButtonEvent ev;
                return buttons_get(ev);
            });
            ui_dirty();
        } else if (option.startsWith("Summarize")) {
            std::vector<String> names = templates_list();
            ui_push(make_menu("Template", names, 0, [=](int t) {
                worker_resummarize(rec_id, names[t]);
                ui_push(make_message("Queued", "The recording will be summarized again with the \"" +
                                                   names[t] + "\" template."));
            }));
        } else if (option.startsWith("Retry")) {
            worker_retry(rec_id);
            ui_push(make_message("Queued", option == "Retry upload" ? "The upload will be retried."
                                                                    : "Processing will be retried."));
        } else if (option == "Delete") {
            ui_push(make_confirm("Delete?", "Delete this recording with its transcript and summary?", [=] {
                if (worker_current_id() == rec_id) {
                    ui_push(make_message("Busy", "This recording is being processed. Try again in a moment."));
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
