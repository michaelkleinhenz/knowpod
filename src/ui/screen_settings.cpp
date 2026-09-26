#include "screens.h"
#include <SD_MMC.h>
#include "audio/audio.h"
#include "hw/clock.h"
#include "hw/power.h"
#include "net/openrouter.h"
#include "net/web.h"
#include "net/wifi.h"
#include "proc/worker.h"
#include "store/config.h"
#include "store/templates.h"

struct Language {
    const char *code;
    const char *name;
};

static const Language LANGUAGES[] = {
    {"", "Automatic"}, {"en", "English"}, {"de", "Deutsch"}, {"fr", "Français"},
    {"es", "Español"}, {"it", "Italiano"}, {"nl", "Nederlands"}, {"pt", "Português"},
    {"sv", "Svenska"}, {"da", "Dansk"}, {"no", "Norsk"}, {"fi", "Suomi"}, {"pl", "Polski"},
};

static const int SLEEP_CHOICES[] = {0, 1, 2, 5, 10, 30};

static String language_name(const String &code)
{
    for (const Language &l : LANGUAGES)
        if (code == l.code) return l.name;
    return code;
}

static String sleep_name(int minutes)
{
    return minutes <= 0 ? String("Never") : String(minutes) + " min";
}

static String device_info()
{
    String s;
    PowerStatus p = power_status();
    s += "Battery: ";
    if (p.battery_percent < 0) s += "none (USB power)";
    else s += String(p.battery_percent) + " %, " + String(p.battery_mv / 1000.0f, 2) + " V" +
              (p.charging ? ", charging" : p.usb_connected ? ", USB" : "");
    s += "\nTime: " + (clock_valid() ? clock_format("%a %d.%m.%Y %H:%M") : String("not set"));
    s += "\nWi-Fi: " + (wifi_connected() ? wifi_ssid() + " (" + wifi_ip() + ")" : String("off"));
    float total = SD_MMC.totalBytes() / 1e9f;
    float free_gb = (SD_MMC.totalBytes() - SD_MMC.usedBytes()) / 1e9f;
    s += "\nSD card: " + String(free_gb, 1) + " GB free of " + String(total, 1) + " GB";
    s += "\nTranscription: " + config_stt_model();
    s += "\nSummary: " + config_llm_model();
    s += "\nOpenRouter key: " + String(config_api_key().isEmpty() ? "missing (/openrouter.txt)" : "set");
    s += "\nBackend: " + (config_backend_enabled() ? config_backend_url() : String("off (no token in config.json)"));
    s += "\nProcessing: " + String(config_processing_backend() ? "backend" : "on device");
    int uploads = worker_pending_uploads();
    if (uploads) s += "\nWaiting for upload: " + String(uploads);
    s += "\nFree memory: " + String(ESP.getFreeHeap() / 1024) + " KB RAM, " +
         String(ESP.getFreePsram() / 1024) + " KB PSRAM";
    return s;
}

static String web_access_info()
{
    return "Browser: " + web_url() + " (or http://knowpod.local/)\n"
           "User: knowpod\nPassword: " + config_web_password() + "\n\n"
           "MCP server (Streamable HTTP):\n" + web_mcp_url() + "\n"
           "Header: Authorization: Bearer " + config_web_password() + "\n\n"
           "Claude Code:\nclaude mcp add --transport http knowpod " + web_mcp_url() +
           " --header \"Authorization: Bearer " + config_web_password() + "\"\n\n"
           "On USB power the device stays awake; on battery it sleeps after the idle time and "
           "the server is unreachable until you wake it.";
}

class SettingsScreen : public Screen {
public:
    void draw() override
    {
        build();
        draw_title("Settings");
        list.draw();
    }

    void on_button(const ButtonEvent &ev) override
    {
        if (list.on_button(ev)) ui_dirty();
        else if (ev.id == BTN_OK && ev.action == BTN_CLICK) run(list.selected);
    }

    const char *hint() override { return "Press: change · Hold press: back"; }

private:
    ListView list;

    void build()
    {
        int sel = list.selected;
        list.items = {
            {"Device info", ""},
            {"Wi-Fi & time sync", wifi_connected() ? "Connected to " + wifi_ssid() : String("Off")},
            {"Processing", config_processing_backend() ? "Backend (" + config_backend_url() + ")"
                                                       : String("On device (OpenRouter)")},
            {"Summary model", config_llm_model()},
            {"Default template", config_default_template()},
            {"Language", language_name(config_stt_language())},
            {"Speaker labels", config_speaker_labels() ? "On (experimental)" : "Off"},
            {"Recording sounds", config_sound_cues() ? "On" : "Off"},
            {"Sleep after", sleep_name(config_sleep_minutes())},
            {"Web access & MCP", web_active() ? web_url()
                                 : config_web_enabled() ? String("On (waiting for Wi-Fi)") : String("Off")},
            {"Test summary model", ""},
        };
        list.selected = sel;
    }

    void run(int item)
    {
        switch (item) {
        case 0:
            ui_push(make_message("Device info", device_info()));
            break;

        case 1: {
            show_progress("Wi-Fi", "Connecting ...");
            String result;
            if (!wifi_connect()) {
                result = config_wifi().empty() ? "No Wi-Fi configured. Add networks to \"wifi\" in /config.json."
                                               : "Connection failed.";
            } else {
                for (int i = 0; i < 50 && !clock_valid(); i++) {
                    clock_poll();
                    delay(100);
                }
                clock_poll();
                result = "Connected to " + wifi_ssid() + ".\n" +
                         (clock_valid() ? "Time: " + clock_format("%d.%m.%Y %H:%M") : String("Time not synced yet."));
            }
            std::vector<WifiNetwork> nets = config_wifi();
            if (!nets.empty()) {
                result += "\n\nNetworks, tried in this order:";
                for (size_t i = 0; i < nets.size(); i++)
                    result += "\n" + String(i + 1) + ". " + nets[i].ssid +
                              (nets[i].ssid == wifi_ssid() ? " (connected)" : "");
                result += "\n\nEdit them on the web page under Wi-Fi.";
            }
            ui_push(make_message("Wi-Fi", result));
            break;
        }

        case 2:
            ui_push(make_menu("Processing", {"On device (OpenRouter)", "Backend service"},
                              config_processing_backend() ? 1 : 0, [](int c) {
                config_set_processing_backend(c == 1);
                if (c == 1 && !config_backend_enabled())
                    ui_push(make_message("Backend processing",
                                         "Recordings are uploaded and transcribed by the backend; the results "
                                         "are downloaded afterwards.\n\nAdd the device token to config.json "
                                         "under \"backend\": \"token\"."));
                worker_kick();
            }));
            break;

        case 3: {
            std::vector<String> models = config_model_choices();
            int sel = 0;
            for (size_t i = 0; i < models.size(); i++)
                if (models[i] == config_llm_model()) sel = i;
            ui_push(make_menu("Summary model", models, sel, [models](int m) {
                config_set_llm_model(models[m]);
            }));
            break;
        }

        case 4: {
            std::vector<String> names = templates_list();
            int sel = 0;
            for (size_t i = 0; i < names.size(); i++)
                if (names[i] == config_default_template()) sel = i;
            ui_push(make_menu("Default template", names, sel, [names](int t) {
                config_set_default_template(names[t]);
            }));
            break;
        }

        case 5: {
            std::vector<String> names;
            int sel = 0;
            for (const Language &l : LANGUAGES) {
                if (config_stt_language() == l.code) sel = names.size();
                names.push_back(l.name);
            }
            ui_push(make_menu("Language", names, sel, [](int l) {
                config_set_stt_language(LANGUAGES[l].code);
            }));
            break;
        }

        case 6:
            config_set_speaker_labels(!config_speaker_labels());
            ui_dirty();
            break;

        case 7:
            config_set_sound_cues(!config_sound_cues());
            if (config_sound_cues()) play_cue(CUE_START);  // let the user hear it
            ui_dirty();
            break;

        case 8: {
            std::vector<String> names;
            int sel = 0;
            for (int m : SLEEP_CHOICES) {
                if (m == config_sleep_minutes()) sel = names.size();
                names.push_back(sleep_name(m));
            }
            ui_push(make_menu("Sleep after", names, sel, [](int s) {
                config_set_sleep_minutes(SLEEP_CHOICES[s]);
            }));
            break;
        }

        case 9:
            if (config_web_enabled()) {
                ui_push(make_menu("Web access & MCP", {"Show connection details", "Turn off"}, 0, [](int c) {
                    if (c == 0) {
                        ui_push(make_message("Web access & MCP",
                                             web_active() ? web_access_info()
                                                          : String("Not running yet; it starts when Wi-Fi is available.")));
                    } else {
                        config_set_web_enabled(false);
                        web_stop();
                    }
                }));
            } else {
                config_set_web_enabled(true);
                show_progress("Web access", "Connecting ...");
                String error;
                if (web_start(error))
                    ui_push(make_message("Web access & MCP", web_access_info()));
                else
                    ui_push(make_message("Web access", error + "\n\nIt starts automatically when Wi-Fi is available."));
            }
            break;

        case 10: {
            show_progress("Test", "Asking " + config_llm_model() + " ...");
            String reply;
            ApiResult r = {false, -1, "No Wi-Fi connection."};
            unsigned long start = millis();
            if (wifi_connect())
                r = openrouter_chat("You are the assistant inside knowpod, a voice recorder with an e-ink screen.",
                                    "Greet the user in two short sentences, one in German and one in French.",
                                    reply);
            ui_push(make_message(r.ok ? "Model reply" : "Model error",
                                 r.ok ? reply + "\n\n(" + String((millis() - start) / 1000.0f, 1) + " s)"
                                      : r.error));
            break;
        }
        }
    }
};

Screen *make_settings()
{
    return new SettingsScreen();
}
