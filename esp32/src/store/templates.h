#pragma once

#include <Arduino.h>
#include <FS.h>
#include <vector>

// Summary templates are Markdown files in /templates on the SD card; the file
// name (without .md) is the template name and the content is the instruction
// for the model. Defaults are created on first boot; add or edit your own.
//
// /glossary.txt lists names and terms (one per line) so the summary spells
// them correctly.

#define TEMPLATES_DIR "/templates"
#define GLOSSARY_PATH "/glossary.txt"

void templates_begin(fs::FS &fs);
std::vector<String> templates_list();
String template_load(const String &name);   // empty if missing
bool template_save(const String &name, const String &content);
bool template_valid_name(const String &name);
String glossary_load();
