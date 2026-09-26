#pragma once

// E-ink user interface: global buttons, recording control, sleep.
void app_begin(bool sd_ok, bool codec_ok);
void app_loop();

// Starts a recording and shows the recording screen (or an error).
void start_recording_from_ui();
