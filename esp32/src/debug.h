#pragma once

// Serial debug console (audio tests).
void debug_begin();
void debug_loop();

// Stall detector: the main loop names the step it is in; a watcher task logs
// that step (and the loop task's state) when the loop stops making progress.
extern const char *volatile debug_stage;
