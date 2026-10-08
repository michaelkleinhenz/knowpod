#ifdef BOARD_EPAPER_154

#include "exio.h"
#include <Wire.h>
#include "board.h"

#define REG_OUTPUT  0x01
#define REG_CONFIG  0x03   // 1 = input, 0 = output

static const uint8_t OUTPUTS = (1 << EXIO_EPD_PWR) | (1 << EXIO_AUDIO_PWR) | (1 << EXIO_PA_CTRL) |
                               (1 << EXIO_LED) | (1 << EXIO_VBAT_HOLD);

static uint8_t out_state = 0;
static bool ok = false;

static bool write_reg(uint8_t reg, uint8_t value)
{
    Wire.beginTransmission(EXIO_ADDR);
    Wire.write(reg);
    Wire.write(value);
    return Wire.endTransmission() == 0;
}

static bool read_reg(uint8_t reg, uint8_t &value)
{
    Wire.beginTransmission(EXIO_ADDR);
    Wire.write(reg);
    if (Wire.endTransmission(false) != 0 || Wire.requestFrom((uint8_t)EXIO_ADDR, (uint8_t)1) != 1) return false;
    value = Wire.read();
    return true;
}

bool exio_read(uint8_t &outputs, uint8_t &config)
{
    return read_reg(REG_OUTPUT, outputs) && read_reg(REG_CONFIG, config);
}

bool exio_begin()
{
    // Everything on except the speaker amplifier: the battery hold must be set
    // right away, or the board switches off once PWR is released. Set the
    // levels before the direction so the outputs never glitch low.
    out_state = OUTPUTS & ~(1 << EXIO_PA_CTRL);
    ok = write_reg(REG_OUTPUT, out_state) && write_reg(REG_CONFIG, (uint8_t)~OUTPUTS);
    if (!ok) Serial.println("TCA9554 init FAILED");
    return ok;
}

void exio_set(uint8_t pin, bool level)
{
    if (level) out_state |= 1 << pin;
    else out_state &= ~(1 << pin);
    if (ok) write_reg(REG_OUTPUT, out_state);
}

#endif // BOARD_EPAPER_154
