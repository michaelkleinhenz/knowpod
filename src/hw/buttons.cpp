#include "buttons.h"
#include "board.h"
#include <driver/rtc_io.h>

// Polls the GPIO buttons every 10 ms from a timer. Up/down send a click on
// press and then auto-repeat (for scrolling); the others send a click on
// release or a long press once held for BTN_LONG_MS. A button already held
// at startup (e.g. the one that woke the device) is ignored until released.

#define POLL_MS        10
#define DEBOUNCE_POLLS 2

struct GpioButton {
    ButtonId id;
    uint8_t  pin;
    bool     repeats;
    bool     active_high;
    bool     pressed;
    bool     ignore;       // held since startup
    uint8_t  stable_polls;
    uint32_t pressed_ms;
    uint32_t last_repeat_ms;
    bool     long_sent;
};

static GpioButton gpio_buttons[] = {
    {BTN_UP,   PIN_KEY_UP,   true,  false},
    {BTN_DOWN, PIN_KEY_DOWN, true,  false},
    {BTN_OK,   PIN_KEY_OK,   false, false},
    {BTN_BOOT, PIN_KEY_BOOT, false, false},
    {BTN_PWR,  PIN_KEY_PWR,  false, true},
};

static QueueHandle_t events;

void buttons_post(ButtonEvent ev)
{
    xQueueSend(events, &ev, 0);
}

bool buttons_get(ButtonEvent &ev, TickType_t wait)
{
    return xQueueReceive(events, &ev, wait) == pdTRUE;
}

const char *button_name(ButtonId id)
{
    switch (id) {
    case BTN_UP:   return "UP";
    case BTN_DOWN: return "DOWN";
    case BTN_OK:   return "OK";
    case BTN_BOOT: return "BOOT";
    case BTN_PWR:  return "PWR";
    }
    return "?";
}

static void poll(void *)
{
    uint32_t now = millis();
    for (GpioButton &b : gpio_buttons) {
        bool level = digitalRead(b.pin) == (b.active_high ? HIGH : LOW);
        if (b.ignore) {
            if (!level) b.ignore = false;
            continue;
        }

        if (level != b.pressed) {
            if (++b.stable_polls < DEBOUNCE_POLLS) continue;
            b.stable_polls = 0;
            b.pressed = level;

            if (b.pressed) {
                b.pressed_ms = b.last_repeat_ms = now;
                b.long_sent = false;
                if (b.repeats) buttons_post({b.id, BTN_CLICK});
            } else if (!b.repeats && !b.long_sent) {
                buttons_post({b.id, BTN_CLICK});
            }
            continue;
        }
        b.stable_polls = 0;
        if (!b.pressed) continue;

        uint32_t held = now - b.pressed_ms;
        if (b.repeats) {
            if (held >= BTN_LONG_MS && now - b.last_repeat_ms >= BTN_REPEAT_MS) {
                b.last_repeat_ms = now;
                buttons_post({b.id, BTN_REPEAT});
            }
        } else if (!b.long_sent && held >= BTN_LONG_MS) {
            b.long_sent = true;
            buttons_post({b.id, BTN_LONG});
        }
    }
}

void buttons_begin()
{
    events = xQueueCreate(16, sizeof(ButtonEvent));
    for (GpioButton &b : gpio_buttons) {
        if (rtc_gpio_is_valid_gpio((gpio_num_t)b.pin))
            rtc_gpio_deinit((gpio_num_t)b.pin);  // deep sleep wake-up leaves them as RTC pins
        pinMode(b.pin, b.active_high ? INPUT_PULLDOWN : INPUT_PULLUP);
    }
    delay(5);
    for (GpioButton &b : gpio_buttons)
        b.ignore = digitalRead(b.pin) == (b.active_high ? HIGH : LOW);

    const esp_timer_create_args_t args = {
        .callback = poll,
        .name     = "buttons",
    };
    esp_timer_handle_t timer;
    esp_timer_create(&args, &timer);
    esp_timer_start_periodic(timer, POLL_MS * 1000);
}
