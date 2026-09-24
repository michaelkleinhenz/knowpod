#include "DEV_Config.h"

static void GPIO_Config(void)
{
    pinMode(EPD_BUSY_PIN, INPUT);
    pinMode(EPD_RST_PIN, OUTPUT);
    pinMode(EPD_DC_PIN, OUTPUT);
    pinMode(EPD_SCK_PIN, OUTPUT);
    pinMode(EPD_MOSI_PIN, OUTPUT);
    pinMode(EPD_CS_PIN, OUTPUT);

    digitalWrite(EPD_CS_PIN, HIGH);
    digitalWrite(EPD_SCK_PIN, LOW);
}

UBYTE DEV_Module_Init(void)
{
    GPIO_Config();
    return 0;
}

void DEV_SPI_WriteByte(UBYTE data)
{
    digitalWrite(EPD_CS_PIN, GPIO_PIN_RESET);
    for (int i = 0; i < 8; i++) {
        if ((data & 0x80) == 0)
            digitalWrite(EPD_MOSI_PIN, GPIO_PIN_RESET);
        else
            digitalWrite(EPD_MOSI_PIN, GPIO_PIN_SET);
        data <<= 1;
        digitalWrite(EPD_SCK_PIN, GPIO_PIN_SET);
        digitalWrite(EPD_SCK_PIN, GPIO_PIN_RESET);
    }
    digitalWrite(EPD_CS_PIN, GPIO_PIN_SET);
}

void DEV_Module_Exit(void)
{
    digitalWrite(EPD_RST_PIN, LOW);
}
