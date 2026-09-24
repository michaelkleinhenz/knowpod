#include "EPD_3in97.h"

static void EPD_3IN97_Reset(void)
{
    DEV_Digital_Write(EPD_RST_PIN, 1);
    DEV_Delay_ms(20);
    DEV_Digital_Write(EPD_RST_PIN, 0);
    DEV_Delay_ms(2);
    DEV_Digital_Write(EPD_RST_PIN, 1);
    DEV_Delay_ms(20);
}

static void EPD_3IN97_SendCommand(UBYTE Reg)
{
    DEV_Digital_Write(EPD_DC_PIN, 0);
    DEV_Digital_Write(EPD_CS_PIN, 0);
    DEV_SPI_WriteByte(Reg);
    DEV_Digital_Write(EPD_CS_PIN, 1);
}

static void EPD_3IN97_SendData(UBYTE Data)
{
    DEV_Digital_Write(EPD_DC_PIN, 1);
    DEV_Digital_Write(EPD_CS_PIN, 0);
    DEV_SPI_WriteByte(Data);
    DEV_Digital_Write(EPD_CS_PIN, 1);
}

static void EPD_3IN97_ReadBusy(void)
{
    Serial.println("e-Paper busy");
    DEV_Delay_ms(100);
    while (DEV_Digital_Read(EPD_BUSY_PIN) == 1) {
        DEV_Delay_ms(10);
    }
    Serial.println("e-Paper busy release");
}

static void EPD_3IN97_TurnOnDisplay(void)
{
    EPD_3IN97_SendCommand(0x22);
    EPD_3IN97_SendData(0xF7);
    EPD_3IN97_SendCommand(0x20);
    EPD_3IN97_ReadBusy();
}

static void EPD_3IN97_TurnOnDisplay_Fast(void)
{
    EPD_3IN97_SendCommand(0x22);
    EPD_3IN97_SendData(0xD7);
    EPD_3IN97_SendCommand(0x20);
    EPD_3IN97_ReadBusy();
}

void EPD_3IN97_Init(void)
{
    EPD_3IN97_Reset();
    EPD_3IN97_ReadBusy();
    EPD_3IN97_SendCommand(0x12); // SWRESET
    EPD_3IN97_ReadBusy();

    EPD_3IN97_SendCommand(0x18);
    EPD_3IN97_SendData(0x80);

    EPD_3IN97_SendCommand(0x0C);
    EPD_3IN97_SendData(0xAE);
    EPD_3IN97_SendData(0xC7);
    EPD_3IN97_SendData(0xC3);
    EPD_3IN97_SendData(0xC0);
    EPD_3IN97_SendData(0x80);

    EPD_3IN97_SendCommand(0x01); // Driver output control
    EPD_3IN97_SendData((EPD_3IN97_HEIGHT - 1) % 256);
    EPD_3IN97_SendData((EPD_3IN97_HEIGHT - 1) / 256);
    EPD_3IN97_SendData(0x02);

    EPD_3IN97_SendCommand(0x3C); // BorderWaveform
    EPD_3IN97_SendData(0x01);

    EPD_3IN97_SendCommand(0x11); // data entry mode
    EPD_3IN97_SendData(0x01);

    EPD_3IN97_SendCommand(0x44); // set Ram-X address start/end
    EPD_3IN97_SendData(0x00);
    EPD_3IN97_SendData(0x00);
    EPD_3IN97_SendData((EPD_3IN97_WIDTH - 1) % 256);
    EPD_3IN97_SendData((EPD_3IN97_WIDTH - 1) / 256);

    EPD_3IN97_SendCommand(0x45); // set Ram-Y address start/end
    EPD_3IN97_SendData((EPD_3IN97_HEIGHT - 1) % 256);
    EPD_3IN97_SendData((EPD_3IN97_HEIGHT - 1) / 256);
    EPD_3IN97_SendData(0x00);
    EPD_3IN97_SendData(0x00);

    EPD_3IN97_SendCommand(0x4E); // set RAM x address count
    EPD_3IN97_SendData(0x00);
    EPD_3IN97_SendData(0x00);
    EPD_3IN97_SendCommand(0x4F); // set RAM y address count
    EPD_3IN97_SendData(0x00);
    EPD_3IN97_SendData(0x00);
    EPD_3IN97_ReadBusy();
}

void EPD_3IN97_Init_Fast(void)
{
    EPD_3IN97_Reset();
    EPD_3IN97_ReadBusy();
    EPD_3IN97_SendCommand(0x12); // SWRESET
    EPD_3IN97_ReadBusy();

    EPD_3IN97_SendCommand(0x0C);
    EPD_3IN97_SendData(0xAE);
    EPD_3IN97_SendData(0xC7);
    EPD_3IN97_SendData(0xC3);
    EPD_3IN97_SendData(0xC0);
    EPD_3IN97_SendData(0x80);

    EPD_3IN97_SendCommand(0x01);
    EPD_3IN97_SendData((EPD_3IN97_HEIGHT - 1) % 256);
    EPD_3IN97_SendData((EPD_3IN97_HEIGHT - 1) / 256);
    EPD_3IN97_SendData(0x02);

    EPD_3IN97_SendCommand(0x11);
    EPD_3IN97_SendData(0x01);

    EPD_3IN97_SendCommand(0x44);
    EPD_3IN97_SendData(0x00);
    EPD_3IN97_SendData(0x00);
    EPD_3IN97_SendData((EPD_3IN97_WIDTH - 1) % 256);
    EPD_3IN97_SendData((EPD_3IN97_WIDTH - 1) / 256);

    EPD_3IN97_SendCommand(0x45);
    EPD_3IN97_SendData((EPD_3IN97_HEIGHT - 1) % 256);
    EPD_3IN97_SendData((EPD_3IN97_HEIGHT - 1) / 256);
    EPD_3IN97_SendData(0x00);
    EPD_3IN97_SendData(0x00);

    EPD_3IN97_SendCommand(0x4E);
    EPD_3IN97_SendData(0x00);
    EPD_3IN97_SendData(0x00);
    EPD_3IN97_SendCommand(0x4F);
    EPD_3IN97_SendData(0x00);
    EPD_3IN97_SendData(0x00);
    EPD_3IN97_ReadBusy();

    EPD_3IN97_SendCommand(0x3C);
    EPD_3IN97_SendData(0x01);

    EPD_3IN97_SendCommand(0x18);
    EPD_3IN97_SendData(0x80);

    EPD_3IN97_SendCommand(0x1A); // fast mode
    EPD_3IN97_SendData(0x6A);
}

void EPD_3IN97_Clear(void)
{
    UWORD Width = (EPD_3IN97_WIDTH % 8 == 0) ? (EPD_3IN97_WIDTH / 8) : (EPD_3IN97_WIDTH / 8 + 1);
    UWORD Height = EPD_3IN97_HEIGHT;

    EPD_3IN97_SendCommand(0x24);
    for (UWORD j = 0; j < Height; j++) {
        for (UWORD i = 0; i < Width; i++) {
            EPD_3IN97_SendData(0xFF);
        }
        DEV_Delay_ms(1);
    }
    EPD_3IN97_SendCommand(0x26);
    for (UWORD j = 0; j < Height; j++) {
        for (UWORD i = 0; i < Width; i++) {
            EPD_3IN97_SendData(0xFF);
        }
        DEV_Delay_ms(1);
    }
    EPD_3IN97_TurnOnDisplay();
}

void EPD_3IN97_Clear_Black(void)
{
    UWORD Width = (EPD_3IN97_WIDTH % 8 == 0) ? (EPD_3IN97_WIDTH / 8) : (EPD_3IN97_WIDTH / 8 + 1);
    UWORD Height = EPD_3IN97_HEIGHT;

    EPD_3IN97_SendCommand(0x24);
    for (UWORD j = 0; j < Height; j++) {
        for (UWORD i = 0; i < Width; i++) {
            EPD_3IN97_SendData(0x00);
        }
        DEV_Delay_ms(1);
    }
    EPD_3IN97_SendCommand(0x26);
    for (UWORD j = 0; j < Height; j++) {
        for (UWORD i = 0; i < Width; i++) {
            EPD_3IN97_SendData(0x00);
        }
        DEV_Delay_ms(1);
    }
    EPD_3IN97_TurnOnDisplay();
}

void EPD_3IN97_Display(const UBYTE *Image)
{
    UWORD Width = (EPD_3IN97_WIDTH % 8 == 0) ? (EPD_3IN97_WIDTH / 8) : (EPD_3IN97_WIDTH / 8 + 1);
    UWORD Height = EPD_3IN97_HEIGHT;

    EPD_3IN97_SendCommand(0x24);
    for (UWORD j = 0; j < Height; j++) {
        for (UWORD i = 0; i < Width; i++) {
            EPD_3IN97_SendData(Image[i + j * Width]);
        }
        DEV_Delay_ms(1);
    }
    EPD_3IN97_TurnOnDisplay();
}

void EPD_3IN97_Display_Base(const UBYTE *Image)
{
    UWORD Width = (EPD_3IN97_WIDTH % 8 == 0) ? (EPD_3IN97_WIDTH / 8) : (EPD_3IN97_WIDTH / 8 + 1);
    UWORD Height = EPD_3IN97_HEIGHT;

    EPD_3IN97_SendCommand(0x24);
    for (UWORD j = 0; j < Height; j++) {
        for (UWORD i = 0; i < Width; i++) {
            EPD_3IN97_SendData(Image[i + j * Width]);
        }
        DEV_Delay_ms(1);
    }
    EPD_3IN97_SendCommand(0x26);
    for (UWORD j = 0; j < Height; j++) {
        for (UWORD i = 0; i < Width; i++) {
            EPD_3IN97_SendData(Image[i + j * Width]);
        }
        DEV_Delay_ms(1);
    }
    EPD_3IN97_TurnOnDisplay();
}

void EPD_3IN97_Display_Fast(const UBYTE *Image)
{
    UWORD Width = (EPD_3IN97_WIDTH % 8 == 0) ? (EPD_3IN97_WIDTH / 8) : (EPD_3IN97_WIDTH / 8 + 1);
    UWORD Height = EPD_3IN97_HEIGHT;

    EPD_3IN97_SendCommand(0x24);
    for (UWORD j = 0; j < Height; j++) {
        for (UWORD i = 0; i < Width; i++) {
            EPD_3IN97_SendData(Image[i + j * Width]);
        }
        DEV_Delay_ms(1);
    }
    EPD_3IN97_TurnOnDisplay_Fast();
}

void EPD_3IN97_Sleep(void)
{
    EPD_3IN97_SendCommand(0x10); // enter deep sleep
    EPD_3IN97_SendData(0x01);
    DEV_Delay_ms(100);
}
