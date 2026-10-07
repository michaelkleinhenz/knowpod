#include "search.h"

#define CONTEXT_CHARS 100

std::vector<SearchHit> search_recordings(const String &query, size_t max_hits)
{
    std::vector<SearchHit> hits;
    String needle = query;
    needle.trim();
    needle.toLowerCase();
    if (needle.isEmpty()) return hits;

    for (const RecordingInfo &r : recordings_list()) {
        for (const char *name : {"summary.md", "transcript.md"}) {
            String content;
            if (!read_file(recording_path(r.id, name), content)) continue;
            String lower = content;
            lower.toLowerCase();
            int pos = lower.indexOf(needle);
            if (pos < 0) continue;

            int from = max(0, pos - CONTEXT_CHARS);
            int end = pos + needle.length();
            int to = min((int)content.length(), end + CONTEXT_CHARS);
            hits.push_back({r, name, content.substring(from, pos), content.substring(pos, end),
                            content.substring(end, to)});
            break;
        }
        if (hits.size() >= max_hits) break;
    }
    return hits;
}
