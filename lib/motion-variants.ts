import type { Variants } from "framer-motion";

/* ── Page / section reveal ── */
export const fadeUp: Variants = {
  hidden: { opacity: 0, y: 24 },
  visible: {
    opacity: 1,
    y: 0,
    transition: { duration: 0.45, ease: [0.33, 1, 0.68, 1] },
  },
};

export const stagger: Variants = {
  hidden: {},
  visible: { transition: { staggerChildren: 0.07 } },
};

export const staggerSlow: Variants = {
  hidden: {},
  visible: { transition: { staggerChildren: 0.04 } },
};

/* ── Card / item appear ── */
export const itemFadeUp: Variants = {
  hidden: { opacity: 0, y: 16 },
  visible: {
    opacity: 1,
    y: 0,
    transition: { duration: 0.38, ease: [0.33, 1, 0.68, 1] },
  },
};

/* ── Overlay (search modal) ── */
export const overlayVariants: Variants = {
  hidden: { opacity: 0 },
  visible: { opacity: 1, transition: { duration: 0.18 } },
  exit:   { opacity: 0, transition: { duration: 0.15 } },
};

export const panelVariants: Variants = {
  hidden: { opacity: 0, scale: 0.96, y: -8 },
  visible: {
    opacity: 1,
    scale: 1,
    y: 0,
    transition: { duration: 0.22, ease: [0.33, 1, 0.68, 1] },
  },
  exit: {
    opacity: 0,
    scale: 0.96,
    y: -8,
    transition: { duration: 0.16 },
  },
};

/* ── Swatch hover ── */
export const swatchHover = {
  rest: { scale: 1 },
  hover: { scale: 1.08, transition: { type: "spring", stiffness: 300, damping: 22 } },
};

/* ── Bar grow (spacing scale) ── */
export const barGrow = (width: number) => ({
  hidden: { width: 0, opacity: 0 },
  visible: {
    width,
    opacity: 0.55,
    transition: { duration: 0.5, ease: [0.33, 1, 0.68, 1], delay: 0.05 },
  },
});

/* ── Press / tap feedback ── */
export const tapScale = { whileTap: { scale: 0.96 } };
