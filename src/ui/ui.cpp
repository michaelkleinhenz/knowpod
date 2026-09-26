#include "ui.h"
#include <memory>
#include "hw/clock.h"
#include "hw/power.h"
#include "net/web.h"
#include "net/wifi.h"
#include "proc/worker.h"
#include "session.h"

static std::vector<std::unique_ptr<Screen>> stack;
static std::vector<std::unique_ptr<Screen>> trash;
static bool dirty = true;
static bool clean = false;

void Screen::on_back()
{
    ui_pop();
}

// ============================================================
// Stack
// ============================================================

void ui_push(Screen *screen)
{
    stack.emplace_back(screen);
    ui_dirty();
}

void ui_pop()
{
    if (stack.size() <= 1) return;
    trash.push_back(std::move(stack.back()));
    stack.pop_back();
    ui_dirty();
}

void ui_replace(Screen *screen)
{
    if (!stack.empty()) {
        trash.push_back(std::move(stack.back()));
        stack.pop_back();
    }
    ui_push(screen);
}

void ui_pop_to_root()
{
    while (stack.size() > 1) ui_pop();
}

Screen *ui_top()     { return stack.empty() ? nullptr : stack.back().get(); }
bool ui_is_root()    { return stack.size() <= 1; }
void ui_collect_garbage() { trash.clear(); }

void ui_dirty(bool clean_refresh)
{
    dirty = true;
    clean |= clean_refresh;
}

// ============================================================
// Frame
// ============================================================

static void draw_battery(int x, int y, int percent, bool charging)
{
    GFXcanvas1 &g = gfx();
    g.drawRect(x, y, 30, 16, INK);
    g.fillRect(x + 30, y + 4, 3, 8, INK);
    if (percent > 0) g.fillRect(x + 2, y + 2, 26 * percent / 100, 12, INK);
    if (charging) {
        // lightning bolt knocked out of the fill
        g.fillTriangle(x + 17, y + 1, x + 10, y + 9, x + 15, y + 9, PAPER);
        g.fillTriangle(x + 13, y + 15, x + 20, y + 7, x + 15, y + 7, PAPER);
    }
}

static void draw_status_bar()
{
    draw_text(MARGIN, 30, clock_format("%H:%M").c_str(), FONT_BOLD);

    // Center: recording or background work
    String center = session_active() ? String("REC") : worker_status_short();
    if (!center.isEmpty()) draw_text_centered(29, center.c_str(), FONT_SMALL);

    PowerStatus p = power_status();
    int x = SCREEN_W - MARGIN - 33;
    if (p.battery_percent >= 0) {
        draw_battery(x, 12, p.battery_percent, p.charging);
        String pct = String(p.battery_percent) + "%";
        x -= text_width(pct.c_str(), FONT_SMALL) + 8;
        draw_text(x, 29, pct.c_str(), FONT_SMALL);
    } else {
        x += 33 - text_width("USB", FONT_SMALL);
        draw_text(x, 29, "USB", FONT_SMALL);
    }
    const char *net = web_active() ? "Web" : wifi_connected() ? "Wi-Fi" : nullptr;
    if (net) {
        x -= text_width(net, FONT_SMALL) + 14;
        draw_text(x, 29, net, FONT_SMALL);
    }
    gfx().fillRect(0, STATUS_H, SCREEN_W, 2, INK);
}

void ui_render(bool force)
{
    if (!dirty && !force) return;
    Screen *top = ui_top();
    display_clear();
    draw_status_bar();
    if (top) {
        top->draw();
        const char *hint = top->hint();
        if (hint && *hint) {
            gfx().fillRect(0, HINT_Y - 28, SCREEN_W, 1, INK);
            draw_text_centered(HINT_Y, hint, FONT_SMALL);
        }
    }
    display_update(clean, top ? top->auto_clean() : true);
    dirty = clean = false;
}

// ============================================================
// Widgets
// ============================================================

void draw_title(const String &title, const String &subtitle)
{
    // Shorten titles that don't fit on one line
    String t = title;
    int max_w = SCREEN_W - 2 * MARGIN;
    if (text_width(t.c_str(), FONT_TITLE) > max_w) {
        while (t.length() > 1 && text_width((t + "...").c_str(), FONT_TITLE) > max_w) {
            int cut = t.length() - 1;
            while (cut > 0 && (t[cut] & 0xC0) == 0x80) cut--;  // UTF-8 boundary
            t.remove(cut);
        }
        t += "...";
    }
    draw_text(MARGIN, TITLE_Y - (subtitle.isEmpty() ? 0 : 12), t.c_str(), FONT_TITLE);
    if (!subtitle.isEmpty()) draw_text(MARGIN, TITLE_Y + 16, subtitle.c_str(), FONT_SMALL);
}

int draw_paragraph(int x, int y, int width, const String &text, Font font, int max_y)
{
    int lh = line_height(font);
    for (const String &line : wrap_text(text, font, width)) {
        if (y + lh > max_y) break;
        draw_text(x, y + font_ascent(font), line.c_str(), font);
        y += lh;
    }
    return y;
}

// ---- ListView ----

void ListView::draw()
{
    GFXcanvas1 &g = gfx();
    if (items.empty()) {
        draw_paragraph(MARGIN, top + 20, SCREEN_W - 2 * MARGIN, empty_text, FONT_BODY, bottom);
        return;
    }
    bool two_lines = false;
    for (const Item &it : items) two_lines |= !it.subtitle.isEmpty();
    int row_h = two_lines ? 84 : 66;
    int rows = max(1, (bottom - top) / row_h);
    int first = (selected / rows) * rows;

    for (int i = first; i < (int)items.size() && i < first + rows; i++) {
        int y = top + (i - first) * row_h;
        bool sel = i == selected;
        uint16_t fg = sel ? PAPER : INK;
        if (sel) g.fillRoundRect(MARGIN - 8, y + 3, SCREEN_W - 2 * MARGIN + 16, row_h - 6, 10, INK);
        else     g.drawRoundRect(MARGIN - 8, y + 3, SCREEN_W - 2 * MARGIN + 16, row_h - 6, 10, INK);

        // Titles are cut to one line
        std::vector<String> lines = wrap_text(items[i].title, FONT_BOLD, SCREEN_W - 2 * MARGIN - 20);
        String title = lines.empty() ? String() : lines[0];
        if (lines.size() > 1) title += "...";
        int ty = two_lines ? y + 12 + font_ascent(FONT_BOLD) : y + (row_h + font_ascent(FONT_BOLD)) / 2;
        draw_text(MARGIN + 6, ty, title.c_str(), FONT_BOLD, fg);
        if (two_lines)
            draw_text(MARGIN + 6, ty + line_height(FONT_SMALL) + 4, items[i].subtitle.c_str(), FONT_SMALL, fg);
    }

    int pages = (items.size() + rows - 1) / rows;
    if (pages > 1) {
        String p = String(first / rows + 1) + "/" + String(pages);
        draw_text(SCREEN_W - MARGIN - text_width(p.c_str(), FONT_SMALL), bottom + 22, p.c_str(), FONT_SMALL);
    }
}

bool ListView::on_button(const ButtonEvent &ev)
{
    if (items.empty()) return false;
    int n = items.size();
    if (ev.id == BTN_UP)   { selected = (selected + n - 1) % n; return true; }
    if (ev.id == BTN_DOWN) { selected = (selected + 1) % n; return true; }
    return false;
}

// ---- TextView ----

void TextView::clear()
{
    text = "";
    starts.clear();
    fonts.clear();
    paginated = false;
    current = 0;
}

void TextView::add(const String &para, Font font)
{
    for (const String &line : wrap_text(para, font, SCREEN_W - 2 * MARGIN)) {
        starts.push_back(text.length());
        fonts.push_back(font);
        text += line;
        text += '\n';
    }
    paginated = false;
}

void TextView::add_markdown(const String &md)
{
    int pos = 0;
    while (pos <= (int)md.length()) {
        int nl = md.indexOf('\n', pos);
        String line = md.substring(pos, nl < 0 ? md.length() : nl);
        pos = nl < 0 ? md.length() + 1 : nl + 1;
        line.trim();
        line.replace("**", "");

        Font font = FONT_BODY;
        if (line.startsWith("#")) {
            while (line.startsWith("#")) line.remove(0, 1);
            line.trim();
            font = FONT_BOLD;
        } else if (line.startsWith("- [ ] ")) {
            line = "[ ] " + line.substring(6);
        } else if (line.startsWith("- ") || line.startsWith("* ")) {
            line = "· " + line.substring(2);
        }
        if (line.startsWith("_") && line.endsWith("_") && line.length() > 1)
            line = line.substring(1, line.length() - 1);
        add(line, font);
    }
    // Drop trailing blank lines
    while (!starts.empty() && text.length() - starts.back() <= 1) {
        text.remove(starts.back());
        starts.pop_back();
        fonts.pop_back();
    }
}

void TextView::paginate()
{
    page_starts.clear();
    page_starts.push_back(0);
    int y = top;
    for (size_t i = 0; i < starts.size(); i++) {
        int lh = line_height((Font)fonts[i]);
        if (y + lh > bottom && y > top) {
            page_starts.push_back(i);
            y = top;
        }
        y += lh;
    }
    paginated = true;
    current = constrain(current, 0, (int)page_starts.size() - 1);
}

int TextView::pages()
{
    if (!paginated) paginate();
    return page_starts.size();
}

void TextView::set_page(int p)
{
    current = constrain(p, 0, pages() - 1);
}

void TextView::draw()
{
    if (!paginated) paginate();
    size_t first = page_starts[current];
    size_t last = current + 1 < (int)page_starts.size() ? page_starts[current + 1] : starts.size();
    int y = top;
    for (size_t i = first; i < last; i++) {
        Font font = (Font)fonts[i];
        size_t end = text.indexOf('\n', starts[i]);
        String line = text.substring(starts[i], end);
        draw_text(MARGIN, y + font_ascent(font), line.c_str(), font);
        y += line_height(font);
    }
}

bool TextView::on_button(const ButtonEvent &ev)
{
    int n = pages();
    if (ev.id == BTN_UP && current > 0)       { current--; return true; }
    if (ev.id == BTN_DOWN && current + 1 < n) { current++; return true; }
    return false;
}
