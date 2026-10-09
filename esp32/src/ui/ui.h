#pragma once

#include <Arduino.h>
#include <functional>
#include <vector>
#include "hw/buttons.h"
#include "ui/display.h"

// Screen stack with a shared status bar and hint line. The top screen gets
// the button events that app.cpp does not handle globally.

#if SCREEN_H >= 400
// 3.97" (480 × 800 portrait)
#define MARGIN          20
#define STATUS_H        44
#define TITLE_Y         95
#define CONTENT_TOP     120
#define HINT_Y          (SCREEN_H - 18)
#define CONTENT_BOTTOM  (HINT_Y - 34)
#else
// 1.54" (200 × 200)
#define MARGIN          8
#define STATUS_H        20
#define TITLE_Y         42
#define CONTENT_TOP     60
#define HINT_Y          (SCREEN_H - 6)
#define CONTENT_BOTTOM  (HINT_Y - 18)
#endif

class Screen {
public:
    virtual ~Screen() = default;
    virtual void draw() = 0;
    virtual void on_button(const ButtonEvent &ev) = 0;
    virtual void on_back();
    virtual void tick() {}
    virtual bool auto_clean() { return true; }
#ifdef HAS_ROCKER
    virtual const char *hint() { return "Press: open · Hold press: back"; }
#else
    virtual const char *hint() { return "PWR: open · Hold BOOT: back"; }
#endif
};

void ui_push(Screen *screen);
void ui_pop();
void ui_replace(Screen *screen);
void ui_pop_to_root();
Screen *ui_top();
bool ui_is_root();

void ui_dirty(bool clean = false);
void ui_render(bool force = false);
void ui_collect_garbage();

// ---- Widgets ----

void draw_title(const String &title, const String &subtitle = String());
String fit_text(const String &text, Font font, int width);   // shortened with "..." to fit
int draw_paragraph(int x, int y, int width, const String &text, Font font, int max_y);

class ListView {
public:
    struct Item {
        String title;
        String subtitle;
    };
    std::vector<Item> items;
    int selected = 0;
    int top = CONTENT_TOP;
    int bottom = CONTENT_BOTTOM;
    String empty_text = "Nothing here yet.";

    void draw();
    bool on_button(const ButtonEvent &ev);
};

class TextView {
public:
    int top = CONTENT_TOP;
    int bottom = CONTENT_BOTTOM;

    void clear();
    void add(const String &text, Font font = FONT_BODY);
    void add_markdown(const String &md);
    void draw();
    bool on_button(const ButtonEvent &ev);
    int page() const { return current; }
    int pages();
    void set_page(int p);

private:
    String text;
    std::vector<uint32_t> starts;
    std::vector<uint8_t> fonts;
    std::vector<uint32_t> page_starts;
    bool paginated = false;
    int current = 0;
    void paginate();
};
