#pragma once

#if defined(BOARD_EPAPER_397)
#include "board_397.h"
#elif defined(BOARD_EPAPER_154G)
#include "board_154g.h"
#else
#error "Define BOARD_EPAPER_397 or BOARD_EPAPER_154G"
#endif
