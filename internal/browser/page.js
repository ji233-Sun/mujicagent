(() => {
  "use strict";
  // Capture primitives before page scripts run. Page data and the MyGo Eval
  // return channel remain untrusted; no host authority lives in this closure.
  const stringify = JSON.stringify.bind(JSON);
  const define = Object.defineProperty;
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set;
  const textareaSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set;
  const click = HTMLElement.prototype.click;
  const dispatch = EventTarget.prototype.dispatchEvent;
  const EventType = Event;
  const query = Document.prototype.querySelectorAll;
  const style = window.getComputedStyle.bind(window);
  let revision = 1, next = 0, observed = "";
  let refs = new Map();
  const observer = new MutationObserver(() => { revision++; refs.clear(); });
  observer.observe(document, {subtree:true, childList:true, attributes:true, characterData:true});
  document.addEventListener("input", () => { revision++; }, true);
  document.addEventListener("change", () => { revision++; }, true);
  function flush() {
    if (observer.takeRecords().length) { revision++; refs.clear(); }
  }
  const all = selector => Array.from(query.call(document, selector));
  function visible(e) {
    const box = e.getBoundingClientRect(), css = style(e);
    return box.width > 0 && box.height > 0 && box.bottom > 0 && box.right > 0 &&
      box.top < innerHeight && box.left < innerWidth && css.visibility !== "hidden" && css.display !== "none";
  }
  function sensitive(e) {
    const type = (e.type || "").toLowerCase();
    return type === "password" || type === "hidden" || type === "file" ||
      /password|passwd|secret|token|authorization|cookie|credit|card.number|cc-|one.time|otp|api.key/i.test(
        [e.name, e.id, e.autocomplete, e.getAttribute("aria-label")].join(" "));
  }
  function secrets() {
    return all("input,textarea").filter(sensitive).map(e => e.value).filter(v => v);
  }
  function safe(text) {
    text = String(text || "");
    for (const secret of secrets()) text = text.split(secret).join("[redacted]");
    return text.replace(/\b(?:Bearer\s+)?(?:sk-[a-zA-Z0-9_-]{8,}|eyJ[a-zA-Z0-9_-]{12,}(?:\.[a-zA-Z0-9_-]+){1,2})\b/g, "[redacted]")
      .replace(/((?:"|')?(?:[a-z0-9_.-]*(?:token|secret|password|passwd|passphrase|api[_-]?key|access[_-]?key|private[_-]?key)|authorization|proxy-authorization|credentials?)(?:"|')?\s*[:=]\s*)(?:(?:bearer|basic)\s+)?("[^"]*"|'[^']*'|[^\s,;&}\]]+)/gi, "$1[redacted]")
      .replace(/\b(?:Bearer|Basic)\s+[a-zA-Z0-9._~+/-]+=*/gi, "[redacted]");
  }
  function text() {
    const walker = document.createTreeWalker(document.body || document.documentElement, NodeFilter.SHOW_TEXT);
    const parts = [];
    let node, size = 0;
    while ((node = walker.nextNode()) && size < 14000) {
      const e = node.parentElement;
      if (!e || e.closest("script,style,noscript,input,textarea,select,[contenteditable]") || !visible(e)) continue;
      const value = node.textContent.trim();
      if (value) { parts.push(value); size += value.length; }
    }
    return safe(parts.join("\n")).slice(0, 12000);
  }
  function nameText(e) {
    const walker = document.createTreeWalker(e, NodeFilter.SHOW_TEXT);
    let node, value = "", count = 0;
    while ((node = walker.nextNode()) && value.length < 256 && count++ < 1024) {
      if (!node.parentElement.closest("input,textarea,select,script,style")) value += node.textContent + " ";
    }
    return value.trim();
  }
  function name(e) {
    return safe(e.getAttribute("aria-label") || (e.labels && Array.from(e.labels).map(nameText).join(" ")) ||
      (!(e instanceof HTMLInputElement || e instanceof HTMLTextAreaElement || e instanceof HTMLSelectElement) && nameText(e)) ||
      e.getAttribute("placeholder") || e.tagName.toLowerCase()).trim().slice(0, 256);
  }
  function run(action, args) {
    flush();
    if (!document.body || document.readyState === "loading") return {code:"stale"};
    if (action === "observe") {
      refs.clear();
      observed = String(revision);
      const candidates = all("a[href],button,input,textarea,select,[role=button]");
      const elements = [];
      for (const e of candidates) {
        if (elements.length >= 128) break;
        if (!visible(e) || e.disabled || sensitive(e)) continue;
        const fill = e instanceof HTMLTextAreaElement ||
          e instanceof HTMLInputElement && ["text","search","email","url","tel","number"].includes(e.type);
        const clickable = e instanceof HTMLButtonElement || e instanceof HTMLAnchorElement || e.getAttribute("role") === "button" ||
          e instanceof HTMLInputElement && ["button","submit","checkbox","radio"].includes(e.type);
        if (!fill && !clickable) continue;
        const id = String(++next);
        refs.set(id, e);
        elements.push({ID:id, Role:fill ? "textbox" : e instanceof HTMLAnchorElement ? "link" : "button",
          Name:name(e), Click:clickable, Fill:fill});
      }
      return {code:"ok", revision:observed, text:text(), title:safe(document.title).slice(0,256),
        elements, truncated:candidates.length > 128};
    }
    if (action === "scroll") {
      const x = scrollX, y = scrollY;
      window.scrollBy({left:args.x, top:args.y, behavior:"instant"});
      refs.clear(); revision++;
      return {code:"ok", changed:scrollX !== x || scrollY !== y};
    }
    const e = refs.get(args.id);
    const valid = e && e.isConnected && args.revision === observed && observed === String(revision);
    refs.clear();
    if (!valid || !visible(e)) return {code:"stale"};
    if (sensitive(e)) return {code:"sensitive"};
    const before = text(), beforeChecked = e.checked, beforeURL = location.href;
    if (action === "fill") {
      if (e.readOnly || !(e instanceof HTMLInputElement || e instanceof HTMLTextAreaElement)) return {code:"unsupported"};
      const old = e.value;
      (e instanceof HTMLTextAreaElement ? textareaSetter : setter).call(e, args.text);
      dispatch.call(e, new EventType("input", {bubbles:true, composed:true}));
      dispatch.call(e, new EventType("change", {bubbles:true}));
      // A matching DOM value alone does not prove a controlled component
      // consumed input. Require an independently visible page change too.
      return {code:"ok", changed:e.value === args.text && (before !== text() || old === args.text && before.includes(args.text) && args.text !== "")};
    }
    if (action === "click") {
      if (e instanceof HTMLAnchorElement && (e.download || e.target && e.target !== "_self")) return {code:"unsupported"};
      click.call(e);
      return {code:"ok", changed:before !== text() || e.checked !== beforeChecked || location.href !== beforeURL};
    }
    return {code:"unsupported"};
  }
  define(window, "__mujicBrowser", {configurable:false, writable:false, value:(action,args) => {
    try { return stringify(run(action,args || {})); }
    catch { return stringify({code:"engine"}); }
  }});
  // The preload discards this value. Only the injected bundle keeps it in its
  // private world; no pause command is exposed to the page-world handler.
  return {
    pause: () => { flush(); observer.disconnect(); },
    resume: () => observer.observe(document, {subtree:true, childList:true, attributes:true, characterData:true})
  };
})();
