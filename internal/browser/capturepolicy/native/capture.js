"use strict";
// Function parameters come from the injected bundle, never a page bridge.
// Keep the native bitmap call inside this JS invocation, before finally and
// before any queued page MutationObserver, timer or Promise can run.
if (!document.body || document.readyState !== "complete" ||
    location.origin !== expectedOrigin || document.fonts.status !== "loaded" || document.fonts.size)
  return false;
const nodes = [], elements = document.createTreeWalker(document, NodeFilter.SHOW_ELEMENT);
let element;
while (element = elements.nextNode()) {
  nodes.push(element);
  if (nodes.length > 4096) return false;
}
if (document.getAnimations().length) return false;
const forbidden = /^(IFRAME|FRAME|FRAMESET|CANVAS|IMG|PICTURE|VIDEO|AUDIO|SVG|MATH|OBJECT|EMBED|SELECT|PORTAL|MARQUEE)$/;
// Inspect every stylesheet, including otherwise unenumerated pseudos (such
// as scrollbars/backdrops). Cross-origin opaque rules fail closed. Resource
// URLs, custom fonts and counter styles cannot be credential-inspected.
let cssSize = 0;
for (const sheet of [...document.styleSheets, ...document.adoptedStyleSheets]) {
  for (const rule of sheet.cssRules) {
    cssSize += rule.cssText.length;
    if (cssSize > 262144 || /(?:url|paint|image-set|-webkit-image-set|cross-fade|-webkit-cross-fade|image|element)\s*\(|\bcontent\s*:|@import\b|@font-face\b|@counter-style\b/i.test(rule.cssText))
      return false;
  }
}
for (const e of nodes) {
  if (e.namespaceURI !== "http://www.w3.org/1999/xhtml" ||
      forbidden.test(e.tagName) || e.localName.includes("-") ||
      // These two controls have built-in roots which the engine owns and
      // which authors cannot attach. Their live values are cleared below.
      e.shadowRoot && !(e instanceof HTMLInputElement || e instanceof HTMLTextAreaElement) ||
      e instanceof HTMLInputElement && ["file", "image"].includes(e.type)) return false;
  if (/url\s*\(|paint\s*\(/i.test(e.getAttribute("style") || "")) return false;
  // Generated content can contain credentials outside DOM text; visual
  // resources and paint worklets cannot be inspected by this policy.
  for (const pseudo of [null, "::before", "::after", "::marker"]) {
    const s = getComputedStyle(e, pseudo);
    // Text in script/style/noscript is excluded below only because these
    // metadata nodes are normally hidden. CSS can make that text visible.
    if (pseudo === null && /^(HEAD|TITLE|SCRIPT|STYLE|NOSCRIPT)$/.test(e.tagName) &&
        s.display !== "none") return false;
    if (s.display === "list-item" && s.listStyleType !== "none") return false;
    if (s.content && !["none", "normal", '""', "''"].includes(s.content) ||
        /url\(|paint\(/i.test(s.backgroundImage + s.borderImageSource +
          s.listStyleImage + s.maskImage + s.webkitMaskImage)) return false;
  }
}
const controls = nodes.filter(e => e instanceof HTMLInputElement || e instanceof HTMLTextAreaElement);
const secrets = controls.filter(e => e.type === "password" || e.type === "hidden" ||
  /password|passwd|secret|token|authorization|cookie|credit|card.number|cc-|one.time|otp|api.key/i.test(
    [e.name, e.id, e.autocomplete, e.getAttribute("aria-label")].join(" ")))
  .map(e => e.value).filter(Boolean);
// DOM APIs can insert painted text directly under html, outside body.
const walker = document.createTreeWalker(document, NodeFilter.SHOW_TEXT);
const text = [];
let node, size = 0;
while (node = walker.nextNode()) {
  if (!node.parentElement || node.parentElement.closest("script,style,noscript")) continue;
  text.push(node);
  size += node.data.length;
  if (text.length > 8192 || size > 262144) return false;
}
// Combining nodes catches credentials split across markup. A recognized
// credential conservatively redacts all text, avoiding guessed glyph boxes.
const joined = text.map(n => n.data).join("");
const sensitive = secrets.some(s => joined.includes(s)) ||
  /(?:\b(?:Bearer|Basic)\s+[a-zA-Z0-9._~+/-]+=*|\bsk-[a-zA-Z0-9_-]{8,}|\beyJ[a-zA-Z0-9_-]{12,}(?:\.[a-zA-Z0-9_-]+){1,2}\b|(?:token|secret|password|passwd|passphrase|api[_-]?key|access[_-]?key|private[_-]?key|authorization|proxy-authorization|credentials?)["']?\s*[:=]|\b(?:cookie|set-cookie)\s*:|-----BEGIN [A-Z ]*PRIVATE KEY-----|[a-z][a-z0-9+.-]*:\/\/[^/@\s]+:[^/@\s]+@)/i.test(joined);
const undo = [];
const sx = scrollX, sy = scrollY;
references.pause();
try {
  for (const e of controls) {
    const value = e.value, left = e.scrollLeft, top = e.scrollTop;
    const start = e.selectionStart, end = e.selectionEnd, direction = e.selectionDirection;
    const placeholder = e.getAttribute("placeholder");
    undo.push(() => {
      e.value = value;
      if (start !== null) e.setSelectionRange(start, end, direction);
      if (placeholder !== null) e.setAttribute("placeholder", placeholder);
      e.scrollLeft = left; e.scrollTop = top;
    });
    e.value = "";
    if (placeholder !== null) e.removeAttribute("placeholder");
  }
  for (const n of text) {
    if (sensitive || n.parentElement.closest("textarea,[contenteditable]")) {
      const value = n.data;
      undo.push(() => { n.data = value; });
      n.data = "[redacted]";
    }
  }
  // Force style/layout before entering the native painter. The bitmap is
  // immutable before this invocation restores the live DOM.
  void document.body.offsetHeight;
  if (document.getAnimations().length) return false;
  return take(Math.ceil(innerWidth), Math.ceil(innerHeight));
} finally {
  for (let i = undo.length - 1; i >= 0; i--) undo[i]();
  scrollTo({left:sx, top:sy, behavior:"instant"});
  references.resume();
}
