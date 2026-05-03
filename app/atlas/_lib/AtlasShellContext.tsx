"use client";

/**
 * AtlasShellContext — replaces the reference UI's `window.__openLeaf` and
 * `window.__leafOpen` globals with a typed React context.
 *
 * The shell (AtlasShell.tsx) provides this context. Children (atlas, leaf
 * inspector) consume `useAtlasShell()` to call `openLeaf(id)` or check
 * `leafOpen`. SSR-safe: the provider mounts on the client, and consumers
 * default to the inert no-op shape when no provider is present.
 */

import { createContext, useContext, type ReactNode } from "react";

export interface AtlasShellContextShape {
  /** True while a leaf canvas is showing. */
  leafOpen: boolean;
  /** Open the leaf canvas for `flowID` (= our DB flows.id). */
  openLeaf: (flowID: string) => void;
  /** Close any open leaf canvas. */
  closeLeaf: () => void;
}

const NOOP: AtlasShellContextShape = Object.freeze({
  leafOpen: false,
  openLeaf: () => {},
  closeLeaf: () => {},
});

const Ctx = createContext<AtlasShellContextShape>(NOOP);

export function AtlasShellProvider({
  value,
  children,
}: {
  value: AtlasShellContextShape;
  children: ReactNode;
}) {
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useAtlasShell(): AtlasShellContextShape {
  return useContext(Ctx);
}
