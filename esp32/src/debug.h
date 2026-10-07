#pragma once

// Serial debug console (audio and OpenRouter tests).
void debug_begin();
void debug_loop();
bool debug_busy();   // a console command is using the mic
