#pragma once

// E-ink user interface: global buttons, recording control, sleep.
void app_begin(bool sd_ok, bool codec_ok);
void app_loop();

// Counts as user activity (like a button press): keeps the device awake and
// the CPU at its full clock, e.g. for a command on the serial console.
void app_activity();

// Starts a recording and shows the recording screen (or an error).
void start_recording_from_ui();
