#ifndef __EPD_3IN97_H_
#define __EPD_3IN97_H_

#include "DEV_Config.h"

#define EPD_3IN97_WIDTH   800
#define EPD_3IN97_HEIGHT  480

void EPD_3IN97_Init(void);
void EPD_3IN97_Init_Fast(void);
void EPD_3IN97_Clear(void);
void EPD_3IN97_Clear_Black(void);
void EPD_3IN97_Display(const UBYTE *Image);
void EPD_3IN97_Display_Base(const UBYTE *Image);
void EPD_3IN97_Display_Fast(const UBYTE *Image);
void EPD_3IN97_Sleep(void);

#endif
