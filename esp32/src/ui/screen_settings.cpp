#include "screens.h"
#include "store/sdcard.h"
#include "audio/audio.h"
#include "hw/clock.h"
#include "hw/power.h"
#include "net/web.h"
#include "net/wifi.h"
#include "proc/worker.h"
#include "store/config.h"

static const int SLEEP_CHOICES[] = {0, 1, 2, 5, 10, 30};

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
    float total = SDCARD.totalBytes() / 1e9f;
    float free_gb = (SDCARD.totalBytes() - SDCARD.usedBytes()) / 1e9f;
    s += "\nSD card: " + String(free_gb, 1) + " GB free of " + String(total, 1) + " GB";
    s += "\nBackend: " + (config_backend_enabled() ? config_backend_url() : String("off (no token in config.json)"));
    int uploads = worker_pending_uploads();
    if (uploads) s += "\nWaiting for upload: " + String(uploads);
    s += "\nFree memory: " + String(ESP.getFreeHeap() / 1024) + " KB RAM";
#ifdef BOARD_HAS_PSRAM
    s += ", " + String(ESP.getFreePsram() / 1024) + " KB PSRAM";
#endif
    return s;
}

static String web_access_info()
{
    return "Browser: " + web_url() + " (or http://knowpod.local/)\n"
           "User: knowpod\nPassword: " + config_web_password() + "\n\n"
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

#ifdef HAS_ROCKER
    const char *hint() override { return "Press: change · Hold press: back"; }
#else
    const char *hint() override { return "PWR: change · Hold BOOT: back"; }
#endif

private:
    ListView list;

    void build()
    {
        int sel = list.selected;
        list.items = {
            {"Device info", ""},
            {"Wi-Fi & time sync", wifi_connected() ? "Connected to " + wifi_ssid() : String("Off")},
            {"Recording sounds", config_sound_cues() ? "On" : "Off"},
            {"Sleep after", sleep_name(config_sleep_minutes())},
            {"Web access", web_active() ? web_url()
                           : config_web_enabled() ? String("On (waiting for Wi-Fi)") : String("Off")},
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
            config_set_sound_cues(!config_sound_cues());
            if (config_sound_cues()) play_cue(CUE_START);  // let the user hear it
            ui_dirty();
            break;

        case 3: {
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

        case 4:
            if (config_web_enabled()) {
                ui_push(make_menu("Web access", {"Show connection details", "Turn off"}, 0, [](int c) {
                    if (c == 0) {
                        ui_push(make_message("Web access",
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
                    ui_push(make_message("Web access", web_access_info()));
                else
                    ui_push(make_message("Web access", error + "\n\nIt starts automatically when Wi-Fi is available."));
            }
            break;
        }
    }
};

Screen *make_settings()
{
    return new SettingsScreen();
}
