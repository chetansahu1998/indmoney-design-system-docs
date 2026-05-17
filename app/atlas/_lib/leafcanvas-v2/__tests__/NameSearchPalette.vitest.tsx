/**
 * NameSearchPalette.vitest.tsx — UX pins for the Cmd+F overlay (U3b).
 *
 * Surface tested:
 *   - Renders null when closed (no DOM cost when not in use)
 *   - Renders input + filtered list + hint when open
 *   - Filters by substring, case-insensitive
 *   - Arrow Up / Down navigate with wrap-around
 *   - Home / End jump to first / last
 *   - Enter calls onJumpToFrame(highlighted.id) + onClose
 *   - Escape calls onClose without jumping
 *   - Backdrop click closes; click on the card does not
 *   - Mouse-enter on a row updates the highlight
 *   - Empty-frames state shows the "No frames in this leaf" hint
 *   - Empty-match state shows "No match for …" hint
 */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import { act } from "react";

import { NameSearchPalette, type NameSearchPaletteProps } from "../NameSearchPalette";
import type { NamedFrameEntry } from "../camera-actions";

(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const FRAMES: NamedFrameEntry[] = [
  { id: "1:1", label: "Wallet — Cold state" },
  { id: "1:2", label: "Wallet — 1 bank tracked" },
  { id: "1:3", label: "Wallet — 4+ banks tracked" },
  { id: "1:4", label: "Tax Centre — INDstocks" },
  { id: "1:5", label: "Networth — us_v2" },
];

let container: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(
  initialProps: Partial<NameSearchPaletteProps> & Pick<NameSearchPaletteProps, "open">,
): {
  container: HTMLDivElement;
  onClose: ReturnType<typeof vi.fn>;
  onJumpToFrame: ReturnType<typeof vi.fn>;
  rerender: (next: Partial<NameSearchPaletteProps>) => void;
} {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);

  const onClose = vi.fn();
  const onJumpToFrame = vi.fn();
  const { open: initialOpen, ...restInitial } = initialProps;

  function render(p: Partial<NameSearchPaletteProps>): void {
    const merged: NameSearchPaletteProps = {
      open: initialOpen,
      frames: FRAMES,
      onClose,
      onJumpToFrame,
      ...restInitial,
      ...p,
    };
    act(() => {
      root!.render(<NameSearchPalette {...merged} />);
    });
  }

  render({});
  return {
    container: container!,
    onClose,
    onJumpToFrame,
    rerender: render,
  };
}

afterEach(() => {
  if (root) {
    act(() => root!.unmount());
    root = null;
  }
  if (container) {
    container.remove();
    container = null;
  }
});

function getPalette(c: HTMLElement): HTMLElement | null {
  return c.querySelector<HTMLElement>(".leafcv2-search-palette");
}
function getInput(c: HTMLElement): HTMLInputElement {
  const el = c.querySelector<HTMLInputElement>(".leafcv2-search-palette__input");
  if (!el) throw new Error("input not found");
  return el;
}
function getRows(c: HTMLElement): HTMLElement[] {
  return Array.from(c.querySelectorAll<HTMLElement>(".leafcv2-search-palette__row"));
}
function getActiveRow(c: HTMLElement): HTMLElement | null {
  return c.querySelector<HTMLElement>(".leafcv2-search-palette__row--active");
}

function fireKey(target: Element, init: KeyboardEventInit): void {
  act(() => {
    target.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, ...init }));
  });
}

function fireChange(input: HTMLInputElement, value: string): void {
  // React tracks its own value descriptor on the input. Setting
  // input.value directly bypasses that tracker, so React thinks the
  // value didn't change and skips the onChange dispatch. Use the
  // native setter explicitly so React sees the new value before the
  // input event fires.
  const setter = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype,
    "value",
  )?.set;
  act(() => {
    setter?.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

describe("NameSearchPalette — render", () => {
  it("renders nothing when closed", () => {
    const { container } = mount({ open: false });
    expect(getPalette(container)).toBeNull();
  });

  it("renders palette + input + rows when open", () => {
    const { container } = mount({ open: true });
    expect(getPalette(container)).not.toBeNull();
    expect(getInput(container)).not.toBeNull();
    expect(getRows(container).length).toBe(FRAMES.length);
  });

  it("first row is highlighted on open", () => {
    const { container } = mount({ open: true });
    const active = getActiveRow(container);
    expect(active?.getAttribute("data-row-idx")).toBe("0");
  });
});

describe("NameSearchPalette — filtering", () => {
  it("substring match is case-insensitive", () => {
    const { container } = mount({ open: true });
    fireChange(getInput(container), "wallet");
    expect(getRows(container).length).toBe(3);
  });

  it("empty query restores the full list", () => {
    const { container } = mount({ open: true });
    fireChange(getInput(container), "wallet");
    fireChange(getInput(container), "");
    expect(getRows(container).length).toBe(FRAMES.length);
  });

  it("no-match state shows the empty hint", () => {
    const { container } = mount({ open: true });
    fireChange(getInput(container), "no-such-frame");
    expect(getRows(container).length).toBe(0);
    const empty = container.querySelector(".leafcv2-search-palette__empty");
    expect(empty?.textContent).toContain("No match");
  });

  it("empty-frames input state disables typing", () => {
    const { container } = mount({ open: true, frames: [] });
    const input = getInput(container);
    expect(input.disabled).toBe(true);
    expect(container.querySelector(".leafcv2-search-palette__empty")?.textContent).toContain(
      "No frames",
    );
  });
});

describe("NameSearchPalette — keyboard navigation", () => {
  it("ArrowDown advances the highlight", () => {
    const { container } = mount({ open: true });
    fireKey(getInput(container), { key: "ArrowDown" });
    expect(getActiveRow(container)?.getAttribute("data-row-idx")).toBe("1");
  });

  it("ArrowUp from row 0 wraps to the last row", () => {
    const { container } = mount({ open: true });
    fireKey(getInput(container), { key: "ArrowUp" });
    expect(getActiveRow(container)?.getAttribute("data-row-idx")).toBe(
      String(FRAMES.length - 1),
    );
  });

  it("ArrowDown from the last row wraps to row 0", () => {
    const { container } = mount({ open: true });
    fireKey(getInput(container), { key: "End" });
    fireKey(getInput(container), { key: "ArrowDown" });
    expect(getActiveRow(container)?.getAttribute("data-row-idx")).toBe("0");
  });

  it("Home jumps to row 0", () => {
    const { container } = mount({ open: true });
    fireKey(getInput(container), { key: "End" });
    fireKey(getInput(container), { key: "Home" });
    expect(getActiveRow(container)?.getAttribute("data-row-idx")).toBe("0");
  });

  it("End jumps to the last row", () => {
    const { container } = mount({ open: true });
    fireKey(getInput(container), { key: "End" });
    expect(getActiveRow(container)?.getAttribute("data-row-idx")).toBe(
      String(FRAMES.length - 1),
    );
  });
});

describe("NameSearchPalette — activation + close", () => {
  it("Enter calls onJumpToFrame with the highlighted id and onClose", () => {
    const { container, onClose, onJumpToFrame } = mount({ open: true });
    fireKey(getInput(container), { key: "Enter" });
    expect(onJumpToFrame).toHaveBeenCalledWith(FRAMES[0].id);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("Enter respects the filtered list (post-filter index)", () => {
    const { container, onJumpToFrame } = mount({ open: true });
    fireChange(getInput(container), "Tax");
    fireKey(getInput(container), { key: "Enter" });
    expect(onJumpToFrame).toHaveBeenCalledWith("1:4");
  });

  it("Escape closes without jumping", () => {
    const { container, onClose, onJumpToFrame } = mount({ open: true });
    fireKey(getInput(container), { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onJumpToFrame).not.toHaveBeenCalled();
  });

  it("clicking a row activates that row", () => {
    const { container, onJumpToFrame, onClose } = mount({ open: true });
    const rows = getRows(container);
    act(() => {
      rows[2].dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onJumpToFrame).toHaveBeenCalledWith(FRAMES[2].id);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("backdrop click closes the palette", () => {
    const { container, onClose } = mount({ open: true });
    const backdrop = container.querySelector<HTMLElement>(
      ".leafcv2-search-palette__backdrop",
    );
    if (!backdrop) throw new Error("backdrop not found");
    act(() => {
      backdrop.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("clicking inside the palette card does NOT close", () => {
    const { container, onClose } = mount({ open: true });
    const palette = getPalette(container);
    if (!palette) throw new Error("palette card missing");
    act(() => {
      palette.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onClose).not.toHaveBeenCalled();
  });
});

describe("NameSearchPalette — mouse hover highlight", () => {
  it("pointerover-like interaction on a row updates the highlight", () => {
    // React synthesizes onMouseEnter from native mouseover + tracking
    // relatedTarget. Dispatching a bubbling mouseover with no
    // relatedTarget approximates a fresh enter event.
    const { container } = mount({ open: true });
    const rows = getRows(container);
    act(() => {
      rows[3].dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
    });
    expect(getActiveRow(container)?.getAttribute("data-row-idx")).toBe("3");
  });
});

describe("NameSearchPalette — reopen behavior", () => {
  it("re-opening resets the query and highlight to row 0", () => {
    const result = mount({ open: true });
    fireChange(getInput(result.container), "Tax");
    fireKey(getInput(result.container), { key: "ArrowDown" });
    result.rerender({ open: false });
    result.rerender({ open: true });
    const input = getInput(result.container);
    expect(input.value).toBe("");
    expect(getActiveRow(result.container)?.getAttribute("data-row-idx")).toBe("0");
  });
});
