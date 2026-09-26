#pragma once

#include <Arduino.h>
#include <vector>
#include "store/recordings.h"

struct SearchHit {
    RecordingInfo recording;
    String file;      // "summary.md" or "transcript.md"
    String before;    // context around the match
    String match;
    String after;
};

// Case-insensitive (ASCII) search in summaries and transcripts, newest
// recordings first; at most one hit per recording.
std::vector<SearchHit> search_recordings(const String &query, size_t max_hits = 50);
