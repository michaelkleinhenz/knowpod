#pragma once

#if defined(BOARD_EPAPER_397)
#include "board_397.h"
#elif defined(BOARD_EPAPER_154)
#include "board_154.h"
#else
#error "Define BOARD_EPAPER_397 or BOARD_EPAPER_154"
#endif
