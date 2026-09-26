#include "templates.h"
#include <algorithm>
#include "store/recordings.h"

static fs::FS *tpl_fs = nullptr;

struct DefaultTemplate {
    const char *name;
    const char *content;
};

static const DefaultTemplate DEFAULTS[] = {
    {"meeting",
     "Meeting notes.\n"
     "- Start with a 2-3 sentence overview of purpose and outcome.\n"
     "- Then a section per topic discussed, with the key points as bullets.\n"
     "- A 'Decisions' section listing every decision made.\n"
     "- A 'Open questions' section if anything was left unresolved.\n"
     "Put all tasks into action_items with owner and due date when mentioned.\n"},
    {"lecture",
     "Lecture or talk notes for later study.\n"
     "- Start with the main thesis in 2-3 sentences.\n"
     "- Then the key concepts, each with a short explanation and examples given.\n"
     "- Include definitions, formulas and references mentioned.\n"
     "- End with 3-5 review questions.\n"
     "Action items: only assignments or reading explicitly mentioned.\n"},
    {"interview",
     "Interview notes.\n"
     "- Start with who was interviewed and the context, if mentioned.\n"
     "- Then the questions asked, each with a concise summary of the answer.\n"
     "- Quote particularly striking statements verbatim.\n"
     "- End with the main takeaways.\n"},
    {"one-on-one",
     "Notes from a 1:1 conversation.\n"
     "- Sections: 'Updates', 'Challenges', 'Feedback', 'Next steps'.\n"
     "- Keep it short and personal in tone; skip small talk.\n"
     "Put agreed follow-ups into action_items with the responsible person.\n"},
    {"note",
     "A personal voice note or memo.\n"
     "- Clean up the spoken text into well-structured written notes.\n"
     "- Keep all ideas and details; remove filler words and repetitions.\n"
     "- Use headings only if there are several distinct topics.\n"
     "Turn anything the speaker intends to do into action_items.\n"},
};

static String path_of(const String &name)
{
    return String(TEMPLATES_DIR "/") + name + ".md";
}

bool template_valid_name(const String &name)
{
    return recording_valid_id(name);  // same rules: letters, digits, - and _
}

void templates_begin(fs::FS &fs)
{
    tpl_fs = &fs;
    if (fs.exists(TEMPLATES_DIR)) return;

    fs.mkdir(TEMPLATES_DIR);
    for (const DefaultTemplate &t : DEFAULTS) write_file(path_of(t.name), t.content);
    if (!fs.exists(GLOSSARY_PATH))
        write_file(GLOSSARY_PATH, "");
    Serial.println("Created default templates in " TEMPLATES_DIR);
}

std::vector<String> templates_list()
{
    std::vector<String> names;
    File dir = tpl_fs->open(TEMPLATES_DIR);
    File entry;
    while (dir && (entry = dir.openNextFile())) {
        String name = entry.name();
        bool is_dir = entry.isDirectory();
        entry.close();
        if (is_dir || !name.endsWith(".md")) continue;
        names.push_back(name.substring(0, name.length() - 3));
    }
    if (dir) dir.close();
    std::sort(names.begin(), names.end());
    return names;
}

String template_load(const String &name)
{
    String content;
    if (!template_valid_name(name) || !read_file(path_of(name), content)) return String();
    return content;
}

bool template_save(const String &name, const String &content)
{
    if (!template_valid_name(name)) return false;
    return write_file(path_of(name), content);
}

String glossary_load()
{
    String content;
    read_file(GLOSSARY_PATH, content);
    content.trim();
    return content;
}
