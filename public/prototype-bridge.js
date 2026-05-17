/**
 * prototype-bridge.js — Plan 005 Phase A.
 *
 * Tiny bridge a prototype HTML imports so it can communicate with the
 * Atlas shell hosting it in an iframe. Two responsibilities:
 *
 *   1. CLICK → DRD scroll
 *      Every element carrying `data-screen="<id>"` becomes a click target.
 *      On click, posts {source, type:"screen:click", screenId, label} to
 *      window.parent. Atlas listens and scrolls the matching DRD section.
 *
 *   2. FOCUS ← DRD scroll
 *      Listens for {source:"atlas", type:"screen:focus", screenId}. When
 *      received, scrolls the matching [data-screen] element into view and
 *      adds a data-active="true" attribute for ~1500ms so the prototype's
 *      CSS can render a focus ring.
 *
 * Contract:
 *   - Wrap each prototype screen in a clickable wrapper:
 *       <div data-screen="S3" data-screen-label="Trader Mode ON">…</div>
 *   - Or annotate the screen TITLE element:
 *       <h3 data-screen="S3">S3 · Trader Mode ON</h3>
 *
 * Versioning:
 *   - Bumped via `BRIDGE_VERSION` constant. Atlas reads it from the
 *     hello message so we can warn on stale prototypes after a contract
 *     change.
 *
 * Origin validation:
 *   - Posts to `window.parent` with origin "*" so the prototype works
 *     across dev and prod (the parent is trusted because the iframe is
 *     same-origin OR sandboxed). The Atlas listener validates by
 *     checking `event.source === iframe.contentWindow`, not by origin.
 */
(function () {
  "use strict";
  const BRIDGE_VERSION = 1;
  const ATLAS_SOURCE = "atlas";
  const PROTOTYPE_SOURCE = "indmoney-prototype";
  const FOCUS_ATTR = "data-active";
  const FOCUS_HOLD_MS = 1500;

  function send(type, payload) {
    if (typeof window === "undefined" || !window.parent || window.parent === window) {
      return;
    }
    try {
      window.parent.postMessage(
        Object.assign(
          { source: PROTOTYPE_SOURCE, type, bridgeVersion: BRIDGE_VERSION },
          payload || {},
        ),
        "*",
      );
    } catch (_) {
      // postMessage to a closed parent throws — swallow.
    }
  }

  function screenElements() {
    return Array.prototype.slice.call(document.querySelectorAll("[data-screen]"));
  }

  function findScreenElement(screenId) {
    if (!screenId) return null;
    return document.querySelector('[data-screen="' + cssEscape(screenId) + '"]');
  }

  // Minimal cssEscape so we don't pull in a polyfill. screenIds are
  // expected to be ASCII-safe ("S3", "trader-mode") so a strict allowlist
  // is enough — anything outside [A-Za-z0-9_-] gets escaped numerically.
  function cssEscape(s) {
    return String(s).replace(/[^A-Za-z0-9_-]/g, function (c) {
      return "\\" + c.charCodeAt(0).toString(16) + " ";
    });
  }

  function bindClicks() {
    document.addEventListener(
      "click",
      function (e) {
        const target = e.target.closest("[data-screen]");
        if (!target) return;
        const screenId = target.getAttribute("data-screen");
        if (!screenId) return;
        const label =
          target.getAttribute("data-screen-label") ||
          target.textContent.replace(/\s+/g, " ").trim().slice(0, 120);
        send("screen:click", { screenId: screenId, label: label });
      },
      true,
    );
  }

  let activeReset = null;
  function focusScreen(screenId) {
    const el = findScreenElement(screenId);
    if (!el) return false;
    el.scrollIntoView({ behavior: "smooth", block: "center", inline: "nearest" });
    el.setAttribute(FOCUS_ATTR, "true");
    if (activeReset) window.clearTimeout(activeReset);
    activeReset = window.setTimeout(function () {
      el.removeAttribute(FOCUS_ATTR);
      activeReset = null;
    }, FOCUS_HOLD_MS);
    return true;
  }

  function bindMessages() {
    window.addEventListener("message", function (event) {
      const data = event && event.data;
      if (!data || data.source !== ATLAS_SOURCE) return;
      if (data.type === "screen:focus") {
        focusScreen(data.screenId);
      } else if (data.type === "hello?") {
        // Atlas handshake — re-emit the hello so a late mount still
        // catches us.
        sendHello();
      }
    });
  }

  function sendHello() {
    const screens = screenElements().map(function (el) {
      return {
        screenId: el.getAttribute("data-screen"),
        label:
          el.getAttribute("data-screen-label") ||
          el.textContent.replace(/\s+/g, " ").trim().slice(0, 120),
      };
    });
    send("hello", { screens: screens });
  }

  function init() {
    bindClicks();
    bindMessages();
    sendHello();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
