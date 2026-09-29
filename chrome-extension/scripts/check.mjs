// Validates the manifest and syntax-checks the scripts. Run with `make check`.
import { readFileSync, existsSync, readdirSync } from "node:fs";
import { execFileSync } from "node:child_process";

const src = new URL("../src/", import.meta.url).pathname;
const manifest = JSON.parse(readFileSync(src + "manifest.json", "utf8"));
const files = [
  manifest.action?.default_popup,
  manifest.options_ui?.page,
  ...Object.values(manifest.icons ?? {}),
  ...Object.values(manifest.action?.default_icon ?? {}),
];
const missing = files.filter((f) => f && !existsSync(src + f));
if (missing.length) { console.error("Missing files:", missing.join(", ")); process.exit(1); }

for (const f of readdirSync(src).filter((f) => f.endsWith(".js"))) {
  const code = readFileSync(src + f, "utf8");
  const mod = /^\s*(import|export)\s/m.test(code);
  // Syntax check only (nothing is run); ES modules are checked as such via stdin.
  execFileSync(process.execPath, ["--check", ...(mod ? ["--input-type=module"] : []), "-"], { input: code, stdio: ["pipe", "inherit", "inherit"] });
}
console.log(`ok: ${manifest.name} ${manifest.version}`);
