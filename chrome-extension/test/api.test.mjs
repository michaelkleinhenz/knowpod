import test from "node:test";
import assert from "node:assert/strict";
import { buildMarkdown } from "../src/api.js";

const now = new Date("2026-09-29T10:00:00Z");

test("note links to the source and keeps the text verbatim", () => {
  const md = buildMarkdown({ url: "https://e.com/a", title: "A [Title]", text: "Body text." }, now);
  assert.match(md, /^Source: \[A Title\]\(https:\/\/e\.com\/a\)\n\nClipped: 2026-09-29\n\n---\n\nBody text\.$/);
});

test("long pages are cut to the note limit", () => {
  const md = buildMarkdown({ url: "https://e.com", title: "T", text: "x".repeat(200000) }, now);
  assert.ok(md.length <= 100000);
  assert.match(md, /Clip shortened/);
});
