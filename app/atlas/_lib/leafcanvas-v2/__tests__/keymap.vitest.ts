/**
 * keymap.vitest.ts — dispatcher + focus gate + action-match pins (U3).
 *
 * Three surfaces:
 *   1. matchAction — pure event → action name resolution. All hotkey
 *      combinations are pinned so a future "renaming a hotkey to match
 *      Figma" change is one place + one test row.
 *   2. isCanvasKeymapEligible — focus gate predicate. Editable targets
 *      (input/textarea/select/contenteditable) are excluded even when
 *      they sit inside .lc-stage. Elements outside .lc-stage are
 *      excluded regardless.
 *   3. installKeymap → action table dispatch. Verifies the
 *      registered action handler fires on a matching key event when
 *      the focus gate passes; does NOT fire when the gate fails;
 *      held-key tracking (Z, Space) flips on keydown/keyup/blur.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  __getActionTableForTesting,
  __resetKeymapForTesting,
  getHeldKey,
  installKeymap,
  isCanvasKeymapEligible,
  matchAction,
  registerKeymap,
  type ActionTable,
} from "../keymap";

afterEach(() => {
  __resetKeymapForTesting();
  document.body.innerHTML = "";
});

describe("matchAction — camera + zoom", () => {
  it.each<[string, Partial<KeyboardEventInit>, string | null]>([
    ["Shift+1 fits all", { code: "Digit1", shiftKey: true }, "canvas.fit-all"],
    ["Shift+2 fits selection", { code: "Digit2", shiftKey: true }, "canvas.fit-selection"],
    ["Cmd+0 zoom-100", { code: "Digit0", metaKey: true }, "canvas.zoom-100"],
    ["Ctrl+0 zoom-100 (windows)", { code: "Digit0", ctrlKey: true }, "canvas.zoom-100"],
    ["+ zoom-in", { key: "+" }, "canvas.zoom-in"],
    ["= zoom-in (same physical key as +)", { key: "=" }, "canvas.zoom-in"],
    ["- zoom-out", { key: "-" }, "canvas.zoom-out"],
    ["_ zoom-out (shift+- on some layouts)", { key: "_" }, "canvas.zoom-out"],
    ["N next named frame", { code: "KeyN" }, "canvas.next-named-frame"],
    ["Shift+N previous named frame", { code: "KeyN", shiftKey: true }, "canvas.prev-named-frame"],
  ])("%s", (_label, init, expected) => {
    const e = new KeyboardEvent("keydown", init);
    expect(matchAction(e)).toBe(expected);
  });
});

describe("matchAction — selection", () => {
  it.each<[string, Partial<KeyboardEventInit>, string | null]>([
    ["Escape is layered close", { key: "Escape" }, "selection.escape-layered"],
    ["Cmd+A selects all", { code: "KeyA", metaKey: true }, "selection.select-all"],
    ["Tab is next sibling", { key: "Tab" }, "selection.next-sibling"],
    ["Shift+Tab is previous sibling", { key: "Tab", shiftKey: true }, "selection.prev-sibling"],
    ["Enter descends", { key: "Enter" }, "selection.descend"],
    ["Shift+Enter ascends", { key: "Enter", shiftKey: true }, "selection.ascend"],
    ["\\ ascends (Figma alternate)", { key: "\\" }, "selection.ascend"],
  ])("%s", (_label, init, expected) => {
    const e = new KeyboardEvent("keydown", init);
    expect(matchAction(e)).toBe(expected);
  });
});

describe("matchAction — mode + search", () => {
  it("Shift+D toggles dev mode", () => {
    const e = new KeyboardEvent("keydown", { code: "KeyD", shiftKey: true });
    expect(matchAction(e)).toBe("mode.toggle-dev-mode");
  });

  it("H toggles hand tool", () => {
    const e = new KeyboardEvent("keydown", { code: "KeyH" });
    expect(matchAction(e)).toBe("mode.toggle-hand-tool");
  });

  it("Cmd+F opens the search palette", () => {
    const e = new KeyboardEvent("keydown", { code: "KeyF", metaKey: true });
    expect(matchAction(e)).toBe("search.open-palette");
  });

  it("plain F (no modifier) is not a match", () => {
    const e = new KeyboardEvent("keydown", { code: "KeyF" });
    expect(matchAction(e)).toBeNull();
  });
});

describe("matchAction — non-matches", () => {
  it("unmapped keys return null", () => {
    expect(matchAction(new KeyboardEvent("keydown", { code: "KeyQ" }))).toBeNull();
    expect(matchAction(new KeyboardEvent("keydown", { code: "F5" }))).toBeNull();
  });
});

describe("isCanvasKeymapEligible — focus gate", () => {
  let stage: HTMLDivElement;
  let stageInput: HTMLInputElement;
  let outsideButton: HTMLButtonElement;
  let outsideInput: HTMLInputElement;

  beforeEach(() => {
    stage = document.createElement("div");
    stage.className = "lc-stage";
    stage.tabIndex = 0;
    stageInput = document.createElement("input");
    stage.appendChild(stageInput);
    outsideButton = document.createElement("button");
    outsideInput = document.createElement("input");
    document.body.appendChild(stage);
    document.body.appendChild(outsideButton);
    document.body.appendChild(outsideInput);
  });

  it("returns true for the .lc-stage element itself", () => {
    expect(isCanvasKeymapEligible(stage)).toBe(true);
  });

  it("returns true for a non-editable descendant of .lc-stage", () => {
    const inner = document.createElement("div");
    stage.appendChild(inner);
    expect(isCanvasKeymapEligible(inner)).toBe(true);
  });

  it("returns false for an input inside .lc-stage (InlineTextEditor case)", () => {
    expect(isCanvasKeymapEligible(stageInput)).toBe(false);
  });

  it("returns false for contenteditable inside .lc-stage", () => {
    const editable = document.createElement("div");
    editable.contentEditable = "true";
    stage.appendChild(editable);
    expect(isCanvasKeymapEligible(editable)).toBe(false);
  });

  it("returns false for an element outside .lc-stage", () => {
    expect(isCanvasKeymapEligible(outsideButton)).toBe(false);
  });

  it("returns false for an input outside .lc-stage", () => {
    expect(isCanvasKeymapEligible(outsideInput)).toBe(false);
  });

  it("returns false for a textarea inside .lc-stage", () => {
    const ta = document.createElement("textarea");
    stage.appendChild(ta);
    expect(isCanvasKeymapEligible(ta)).toBe(false);
  });
});

describe("registerKeymap + installKeymap — dispatch", () => {
  let stage: HTMLDivElement;

  beforeEach(() => {
    stage = document.createElement("div");
    stage.className = "lc-stage";
    stage.tabIndex = 0;
    document.body.appendChild(stage);
    stage.focus();
  });

  it("fires the registered action when key matches AND target is canvas-eligible", () => {
    const fitAll = vi.fn();
    registerKeymap({ "canvas.fit-all": fitAll });
    const uninstall = installKeymap();
    const event = new KeyboardEvent("keydown", { code: "Digit1", shiftKey: true, bubbles: true });
    stage.dispatchEvent(event);
    expect(fitAll).toHaveBeenCalledTimes(1);
    uninstall();
  });

  it("does NOT fire when target is outside .lc-stage", () => {
    const fitAll = vi.fn();
    const outside = document.createElement("button");
    document.body.appendChild(outside);
    registerKeymap({ "canvas.fit-all": fitAll });
    const uninstall = installKeymap();
    const event = new KeyboardEvent("keydown", { code: "Digit1", shiftKey: true, bubbles: true });
    outside.dispatchEvent(event);
    expect(fitAll).not.toHaveBeenCalled();
    uninstall();
  });

  it("does NOT fire when target is an input inside .lc-stage", () => {
    const fitAll = vi.fn();
    const input = document.createElement("input");
    stage.appendChild(input);
    registerKeymap({ "canvas.fit-all": fitAll });
    const uninstall = installKeymap();
    const event = new KeyboardEvent("keydown", { code: "Digit1", shiftKey: true, bubbles: true });
    input.dispatchEvent(event);
    expect(fitAll).not.toHaveBeenCalled();
    uninstall();
  });

  it("does NOT fire when no handler is registered for the action", () => {
    const fitAll = vi.fn();
    // Register a different action, not fit-all.
    registerKeymap({ "canvas.fit-selection": fitAll });
    const uninstall = installKeymap();
    const event = new KeyboardEvent("keydown", { code: "Digit1", shiftKey: true, bubbles: true });
    stage.dispatchEvent(event);
    expect(fitAll).not.toHaveBeenCalled();
    uninstall();
  });

  it("preventDefault is called on a matched + dispatched event", () => {
    registerKeymap({ "canvas.fit-all": vi.fn() });
    const uninstall = installKeymap();
    const event = new KeyboardEvent("keydown", {
      code: "Digit1",
      shiftKey: true,
      bubbles: true,
      cancelable: true,
    });
    stage.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(true);
    uninstall();
  });

  it("preventDefault is NOT called when the gate fails", () => {
    registerKeymap({ "canvas.fit-all": vi.fn() });
    const uninstall = installKeymap();
    const outside = document.createElement("button");
    document.body.appendChild(outside);
    const event = new KeyboardEvent("keydown", {
      code: "Digit1",
      shiftKey: true,
      bubbles: true,
      cancelable: true,
    });
    outside.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false);
    uninstall();
  });

  it("registerKeymap can be called again to replace the table", () => {
    const first = vi.fn();
    const second = vi.fn();
    registerKeymap({ "canvas.fit-all": first });
    registerKeymap({ "canvas.fit-all": second });
    const uninstall = installKeymap();
    stage.dispatchEvent(
      new KeyboardEvent("keydown", { code: "Digit1", shiftKey: true, bubbles: true }),
    );
    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledTimes(1);
    uninstall();
  });

  it("registerKeymap's returned unregister fn clears the table", () => {
    const unregister = registerKeymap({ "canvas.fit-all": vi.fn() });
    expect(__getActionTableForTesting()["canvas.fit-all"]).toBeDefined();
    unregister();
    expect(__getActionTableForTesting()["canvas.fit-all"]).toBeUndefined();
  });
});

describe("installKeymap — held transient modes (Z, Space)", () => {
  let stage: HTMLDivElement;
  beforeEach(() => {
    stage = document.createElement("div");
    stage.className = "lc-stage";
    stage.tabIndex = 0;
    document.body.appendChild(stage);
    stage.focus();
  });

  it("Z keydown sets the held flag; keyup clears it", () => {
    const uninstall = installKeymap();
    expect(getHeldKey()).toBeNull();
    window.dispatchEvent(new KeyboardEvent("keydown", { code: "KeyZ" }));
    expect(getHeldKey()).toBe("z");
    window.dispatchEvent(new KeyboardEvent("keyup", { code: "KeyZ" }));
    expect(getHeldKey()).toBeNull();
    uninstall();
  });

  it("Space keydown sets the flag; Space takes priority over Z when both held", () => {
    const uninstall = installKeymap();
    window.dispatchEvent(new KeyboardEvent("keydown", { code: "KeyZ" }));
    window.dispatchEvent(new KeyboardEvent("keydown", { code: "Space" }));
    expect(getHeldKey()).toBe("space");
    window.dispatchEvent(new KeyboardEvent("keyup", { code: "Space" }));
    expect(getHeldKey()).toBe("z");
    uninstall();
  });

  it("window blur clears all held keys (user alt-tabs away mid-drag)", () => {
    const uninstall = installKeymap();
    window.dispatchEvent(new KeyboardEvent("keydown", { code: "KeyZ" }));
    expect(getHeldKey()).toBe("z");
    window.dispatchEvent(new Event("blur"));
    expect(getHeldKey()).toBeNull();
    uninstall();
  });

  it("held-key tracking ignores focus gate (releases register even with focus outside canvas)", () => {
    const uninstall = installKeymap();
    window.dispatchEvent(new KeyboardEvent("keydown", { code: "KeyZ" }));
    expect(getHeldKey()).toBe("z");
    // Even if focus moves outside canvas, releasing Z should clear the flag
    // — otherwise a user that tabs away mid-hold would leave a sticky state.
    document.body.appendChild(document.createElement("input")).focus();
    window.dispatchEvent(new KeyboardEvent("keyup", { code: "KeyZ" }));
    expect(getHeldKey()).toBeNull();
    uninstall();
  });
});

describe("installKeymap — HMR + lifecycle", () => {
  it("uninstall removes listeners (subsequent keys do not fire action)", () => {
    const stage = document.createElement("div");
    stage.className = "lc-stage";
    stage.tabIndex = 0;
    document.body.appendChild(stage);
    stage.focus();
    const fitAll = vi.fn();
    registerKeymap({ "canvas.fit-all": fitAll });
    const uninstall = installKeymap();
    uninstall();
    stage.dispatchEvent(
      new KeyboardEvent("keydown", { code: "Digit1", shiftKey: true, bubbles: true }),
    );
    expect(fitAll).not.toHaveBeenCalled();
  });

  it("HMR guard flag is set", () => {
    expect(
      (globalThis as unknown as { __lcKeymapWired?: boolean }).__lcKeymapWired,
    ).toBe(true);
  });
});

describe("ActionTable type smoke", () => {
  it("an empty action table is a valid registration", () => {
    const table: ActionTable = {};
    expect(() => registerKeymap(table)).not.toThrow();
  });
});
