#pragma once

#include <Arduino.h>
#include <functional>
#include <vector>
#include "hw/buttons.h"
#include "ui/display.h"

// Screen stack with a shared status bar and hint line. The top screen gets
// the button events that app.cpp does not handle globally; BOOT click or a
// long rocker press calls on_back(), which pops the screen by default.

#define MARGIN          20
#define STATUS_H        44
#define TITLE_Y         95
#define CONTENT_TOP     120
#define HINT_Y          (SCREEN_H - 18)
#define CONTENT_BOTTOM  (HINT_Y - 34)

class Screen {
public:
    virtual ~Screen() = default;
    virtual void draw() = 0;
    virtual void on_button(const ButtonEvent &ev) = 0;
    virtual void on_back();
    virtual void tick() {}
    virtual bool auto_clean() { return true; }   // allow periodic ghost-clearing refreshes
    virtual const char *hint() { return "Press: open · Hold press: back"; }
};

void ui_push(Screen *screen);
void ui_pop();                  // deferred delete, safe to call from the screen itself
void ui_replace(Screen *screen);
void ui_pop_to_root();
Screen *ui_top();
bool ui_is_root();

void ui_dirty(bool clean = false);
void ui_render(bool force = false);   // draws if dirty
void ui_collect_garbage();

// ---- Widgets ----

void draw_title(const String &title, const String &subtitle = String());
// Wrapped text; returns the y below it.
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
    bool on_button(const ButtonEvent &ev);   // up/down; true if handled
};

// Paged, word-wrapped text with a simple Markdown subset
// (# headings, - bullets, **bold** markers are stripped).
class TextView {
public:
    int top = CONTENT_TOP;
    int bottom = CONTENT_BOTTOM;

    void clear();
    void add(const String &text, Font font = FONT_BODY);
    void add_markdown(const String &md);
    void draw();
    bool on_button(const ButtonEvent &ev);   // up/down pages; true if handled
    int page() const { return current; }
    int pages();
    void set_page(int p);

private:
    String text;                    // wrapped lines, '\n' separated
    std::vector<uint32_t> starts;   // line start offsets into text
    std::vector<uint8_t> fonts;
    std::vector<uint32_t> page_starts;
    bool paginated = false;
    int current = 0;
    void paginate();
};
