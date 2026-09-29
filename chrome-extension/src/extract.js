// Injected into the page (chrome.scripting.executeScript): returns the page's readable text as
// Markdown. The last expression is the result. Prefers a selection, then <article>/<main>,
// then the largest text block, then the body.
(() => {
  const SKIP = new Set(["SCRIPT", "STYLE", "NOSCRIPT", "TEMPLATE", "IFRAME", "SVG", "CANVAS", "FORM",
    "NAV", "FOOTER", "ASIDE", "HEADER", "BUTTON", "SELECT", "INPUT", "TEXTAREA", "DIALOG"]);
  const NOISE = /(^|[\s_-])(nav|menu|sidebar|footer|comment|cookie|consent|banner|advert|promo|share|social|popup|modal|related)([\s_-]|$)/i;

  const hidden = (el) => {
    if (el.hidden || el.getAttribute("aria-hidden") === "true") return true;
    const s = getComputedStyle(el);
    return s.display === "none" || s.visibility === "hidden";
  };
  const squash = (s) => s.replace(/\s+/g, " ");

  function inline(node) {
    let out = "";
    for (const c of node.childNodes) out += inlineNode(c);
    return out;
  }

  function inlineNode(c) {
    if (c.nodeType === 3) return squash(c.textContent);
    if (c.nodeType !== 1 || SKIP.has(c.tagName.toUpperCase()) || hidden(c)) return "";
    const t = c.tagName.toUpperCase();
    if (t === "BR") return "\n";
    const inner = inline(c);
    if (!inner.trim()) return inner;
    if (t === "A" && /^https?:/.test(c.href)) return `[${inner.trim()}](${c.href})`;
    if (t === "STRONG" || t === "B") return `**${inner.trim()}**`;
    if (t === "EM" || t === "I") return `*${inner.trim()}*`;
    if (t === "CODE") return "`" + inner.trim() + "`";
    return inner;
  }

  const BLOCKS = new Set(["P", "DIV", "SECTION", "ARTICLE", "MAIN", "UL", "OL", "LI", "BLOCKQUOTE", "PRE",
    "H1", "H2", "H3", "H4", "H5", "H6", "TABLE", "TR", "FIGURE", "FIGCAPTION", "DL", "DT", "DD", "HR"]);

  function blocks(node, out, depth = 0) {
    let run = "";
    const flush = () => {
      const t = run.replace(/[ \t]+\n/g, "\n").trim();
      if (t) out.push(t);
      run = "";
    };
    for (const c of node.childNodes) {
      if (c.nodeType === 3) { run += squash(c.textContent); continue; }
      if (c.nodeType !== 1) continue;
      const tag = c.tagName.toUpperCase();
      if (SKIP.has(tag) || hidden(c)) continue;
      if (!BLOCKS.has(tag)) { run += inlineNode(c); continue; }
      flush();
      if (/^H[1-6]$/.test(tag)) {
        const t = inline(c).trim();
        if (t) out.push("#".repeat(Math.min(+tag[1] + 1, 6)) + " " + t);
      } else if (tag === "PRE") {
        const t = c.textContent.replace(/\n+$/, "");
        if (t.trim()) out.push("```\n" + t + "\n```");
      } else if (tag === "BLOCKQUOTE") {
        const inner = [];
        blocks(c, inner, depth + 1);
        if (inner.length) out.push(inner.join("\n\n").replace(/^/gm, "> "));
      } else if (tag === "UL" || tag === "OL") {
        const items = [];
        let n = 1;
        for (const li of c.children) {
          if (li.tagName !== "LI" || hidden(li)) continue;
          const inner = [];
          blocks(li, inner, depth + 1);
          if (!inner.length) continue;
          const mark = tag === "OL" ? `${n++}. ` : "- ";
          items.push(mark + inner.join("\n").replace(/\n/g, "\n  "));
        }
        if (items.length) out.push(items.join("\n"));
      } else if (tag === "HR") {
        out.push("---");
      } else if (tag === "TR") {
        const cells = [...c.children].map((x) => inline(x).trim()).filter(Boolean);
        if (cells.length) out.push(cells.join(" | "));
      } else {
        blocks(c, out, depth + 1);
      }
    }
    flush();
  }

  // The best content root: an explicit article/main, else the element holding the most paragraph text.
  function root() {
    const explicit = document.querySelector("article, [role=main], main, [itemprop=articleBody]");
    if (explicit && explicit.innerText && explicit.innerText.length > 500) return explicit;
    let best = null, bestLen = 0;
    for (const el of document.body.querySelectorAll("div, section")) {
      if (hidden(el) || NOISE.test(el.className + " " + el.id)) continue;
      let len = 0;
      for (const p of el.querySelectorAll(":scope > p, :scope > * > p")) len += p.textContent.trim().length;
      if (len > bestLen) { best = el; bestLen = len; }
    }
    return bestLen > 500 ? best : explicit || document.body;
  }

  let text = "";
  const sel = window.getSelection();
  if (sel && !sel.isCollapsed && sel.toString().trim()) {
    const box = document.createElement("div");
    for (let i = 0; i < sel.rangeCount; i++) box.appendChild(sel.getRangeAt(i).cloneContents());
    // Detached fragments have no computed style, so read them as plain structure.
    const out = [];
    blocks(box, out);
    text = out.join("\n\n") || sel.toString().trim();
  } else {
    const out = [];
    blocks(root(), out);
    text = out.join("\n\n");
  }
  text = text.replace(/\n{3,}/g, "\n\n").trim();

  const title = (document.querySelector("meta[property='og:title']")?.content || document.title || "").trim();
  return { title, url: location.href, text, selection: !!(sel && !sel.isCollapsed) };
})();
