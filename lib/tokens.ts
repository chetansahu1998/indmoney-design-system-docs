// Resolved design tokens from Field Design System
// Source: Base.Mode 1.tokens.json + Semantic.Value.tokens.json + text.styles.tokens.json

// ── Base palette ──────────────────────────────────────────────────────────────
export const base = {
  colour: {
    grey: {
      50:   "#fcfcfd",
      100:  "#f9f9fb",
      200:  "#f2f3f7",
      300:  "#eaecf0",
      400:  "#d0d4dd",
      500:  "#989fb3",
      600:  "#666d85",
      700:  "#475067",
      800:  "#343d54",
      900:  "#1d2539",
      1000: "#101628",
    },
    brandBlue: {
      50:   "#f5faff",
      100:  "#ebf4ff",
      200:  "#d6e9ff",
      300:  "#bddbff",
      400:  "#85bdff",
      500:  "#3392ff",
      600:  "#0f7eff",
      700:  "#0f61ff",
      800:  "#0a49b8",
      900:  "#082f8c",
      1000: "#001f6b",
    },
    noon: {
      50:   "#fefeeb",
      100:  "#fefdc8",
      200:  "#fff7c2",
      300:  "#fff77a",
      400:  "#feee00",
      500:  "#ffd91a",
      600:  "#f5c400",
      700:  "#cc8500",
      800:  "#a36200",
      900:  "#804600",
      1000: "#522c00",
    },
    supermall: {
      50:   "#f6f6fe",
      100:  "#edeefd",
      200:  "#dbdcfa",
      300:  "#c0c2f6",
      400:  "#9fa2ef",
      500:  "#757adb",
      600:  "#4f52d4",
      700:  "#3536da",
      800:  "#2122b8",
      900:  "#19198a",
      1000: "#12136d",
    },
    red: {
      50:   "#fff5f5",
      100:  "#fff0f0",
      200:  "#fed2d2",
      300:  "#fdb5b5",
      400:  "#fc9090",
      500:  "#f75555",
      600:  "#ef2828",
      700:  "#d92626",
      800:  "#a81a1a",
      900:  "#7a1212",
      1000: "#4e0a0a",
    },
    green: {
      50:   "#f6fefb",
      100:  "#e3fcf2",
      200:  "#cbf6e5",
      300:  "#adf0d5",
      400:  "#71d6ad",
      500:  "#26b57c",
      600:  "#12a168",
      700:  "#0f8857",
      800:  "#0b623f",
      900:  "#07422a",
      1000: "#082b1d",
    },
    orange: {
      50:   "#fffaf5",
      100:  "#fff1e0",
      200:  "#fee7cd",
      300:  "#fedbb4",
      400:  "#ffcb8f",
      500:  "#ffa852",
      600:  "#fd8835",
      700:  "#e5641a",
      800:  "#ae3a13",
      900:  "#73260d",
      1000: "#491604",
    },
    white: "#ffffff",
    black: "#000000",
    alphaLight80: "#ffffffcc",
    alphaDark24: "#0000003d",
    alphaDark80: "#000000cc",
  },
} as const;

// ── Semantic tokens ───────────────────────────────────────────────────────────
export const semantic = {
  colour: {
    textNIcon: {
      primary:         "#1d2539",  // grey.900
      secondary:       "#475067",  // grey.700
      tertiary:        "#666d85",  // grey.600
      muted:           "#989fb3",  // grey.500
      onSurfaceBold:   "#ffffff",  // white
      onSurfaceSubtle: "#ffffffcc",// alpha-light.80
      action:          "#0f61ff",  // brand-blue.700
      supermall:       "#2122b8",  // supermall.800
      error:           "#d92626",  // red.700
      warning:         "#e5641a",  // orange.700
      yellowLight:     "#feee00",  // noon.400
      yellowDark:      "#a36200",  // noon.800
      success:         "#0f8857",  // green.700
    },
    surface: {
      primary:           "#ffffff",  // white
      secondary:         "#f9f9fb",  // grey.100
      tertiary:          "#f2f3f7",  // grey.200
      muted:             "#eaecf0",  // grey.300
      tertiaryInverted:  "#343d54",  // grey.800
      secondaryInverted: "#1d2539",  // grey.900
      primaryInverted:   "#101628",  // grey.1000
      actionSubtle:      "#ebf4ff",  // brand-blue.100
      actionBold:        "#0f7eff",  // brand-blue.600
      supermallSubtle:   "#edeefd",  // supermall.100
      supermallBold:     "#2122b8",  // supermall.800
      errorSubtle:       "#fff0f0",  // red.100
      errorBold:         "#d92626",  // red.700
      warningSubtle:     "#fff1e0",  // orange.100
      warningBold:       "#e5641a",  // orange.700
      yellowSubtle:      "#fefdc8",  // noon.100
      yellowMild:        "#fff7c2",  // noon.200
      brandPrimary:      "#feee00",  // noon.400
      yellowBold:        "#f5c400",  // noon.600
      successSubtle:     "#e3fcf2",  // green.100
      successBold:       "#0f8857",  // green.700
      overlaySubtle:     "#0000003d",// alpha-dark.24
      overlayBold:       "#000000cc",// alpha-dark.80
    },
    border: {
      primary:   "#eaecf0",  // grey.300
      subtle:    "#f2f3f7",  // grey.200
      bold:      "#d0d4dd",  // grey.400
      action:    "#bddbff",  // brand-blue.300
      supermall: "#c0c2f6",  // supermall.300
      error:     "#fdb5b5",  // red.300
      warning:   "#fedbb4",  // orange.300
      yellow:    "#fff7c2",  // noon.200
      success:   "#adf0d5",  // green.300
    },
  },
} as const;

// ── Typography ────────────────────────────────────────────────────────────────
export const typography = {
  fontFamily: {
    primary: '"Noontree", -apple-system, BlinkMacSystemFont, sans-serif',
    mono:    '"SF Mono", "Fira Code", "Menlo", monospace',
  },

  heading: [
    { name: "H40", size: 40, lineHeight: 48, letterSpacing: -0.25 },
    { name: "H32", size: 32, lineHeight: 40, letterSpacing: -0.25 },
    { name: "H28", size: 28, lineHeight: 36, letterSpacing: -0.25 },
    { name: "H24", size: 24, lineHeight: 32, letterSpacing: -0.25 },
    { name: "H20", size: 20, lineHeight: 28, letterSpacing: -0.25 },
    { name: "H18", size: 18, lineHeight: 24, letterSpacing: -0.15 },
    { name: "H16", size: 16, lineHeight: 20, letterSpacing: -0.15 },
  ],

  body: [
    { name: "B16", size: 16, lineHeight: 20, letterSpacing: -0.15 },
    { name: "B14", size: 14, lineHeight: 18, letterSpacing: -0.10 },
    { name: "B12", size: 12, lineHeight: 16, letterSpacing: -0.10 },
    { name: "B11", size: 11, lineHeight: 14, letterSpacing: -0.10 },
  ],

  action: [
    { name: "A17", size: 17, lineHeight: 24, letterSpacing: -0.25 },
    { name: "A16", size: 16, lineHeight: 24, letterSpacing:  0    },
    { name: "A14", size: 14, lineHeight: 20, letterSpacing:  0    },
    { name: "A12", size: 12, lineHeight: 16, letterSpacing:  0    },
  ],
} as const;

// ── Motion ────────────────────────────────────────────────────────────────────
export const motion = {
  spring: [
    {
      token:    "motion.spring.fast",
      name:     "Spring_24",
      stiffness: 300,
      damping:   24,
      duration:  "~0.50s",
      feel:     "Snappy, slightly bouncy",
      rn:       "withSpring(value, { stiffness: 300, damping: 24, mass: 1 })",
      android:  "spring(stiffness = 300f, dampingRatio = 0.80f)",
    },
    {
      token:    "motion.spring.standard",
      name:     "Spring_26",
      stiffness: 300,
      damping:   26,
      duration:  "~0.53s",
      feel:     "Balanced, smooth",
      rn:       "withSpring(value, { stiffness: 300, damping: 26, mass: 1 })",
      android:  "spring(stiffness = 300f, dampingRatio = 0.85f)",
    },
    {
      token:    "motion.spring.heavy",
      name:     "Spring_28",
      stiffness: 300,
      damping:   28,
      duration:  "~0.55s",
      feel:     "Heavier, more controlled",
      rn:       "withSpring(value, { stiffness: 300, damping: 28, mass: 1 })",
      android:  "spring(stiffness = 300f, dampingRatio = 0.90f)",
    },
  ],

  opacity: {
    token:    "motion.opacity.standard",
    duration: 200,
    easing:   "cubic-bezier(0.33, 1, 0.68, 1)",
    label:    "Ease Out – Cubic",
    rnReanimated: "withTiming(1, { duration: 200, easing: Easing.bezier(0.33, 1, 0.68, 1) })",
    rnAnimated:   "Animated.timing(opacity, { toValue: 1, duration: 200, easing: Easing.bezier(0.33, 1, 0.68, 1), useNativeDriver: true }).start()",
  },

  scale: {
    token:       "motion.scale.press",
    scaleTo:     0.96,
    duration:    200,
    easing:      "cubic-bezier(0.65, 0, 0.35, 1)",
    easingLabel: "Ease In Out – Cubic",
    startDelay:  0,
    note:        "On touch up, animate back to 100%",
  },
} as const;

// ── Spacing ───────────────────────────────────────────────────────────────────
export const spacing = {
  scale: [
    { token: "space.0",  px: 0  },
    { token: "space.1",  px: 1  },
    { token: "space.2",  px: 2  },
    { token: "space.4",  px: 4  },
    { token: "space.6",  px: 6  },
    { token: "space.8",  px: 8  },
    { token: "space.10", px: 10 },
    { token: "space.12", px: 12 },
    { token: "space.14", px: 14 },
    { token: "space.16", px: 16 },
    { token: "space.18", px: 18 },
    { token: "space.20", px: 20 },
    { token: "space.24", px: 24 },
    { token: "space.28", px: 28 },
    { token: "space.32", px: 32 },
    { token: "space.36", px: 36 },
    { token: "space.40", px: 40 },
    { token: "space.44", px: 44 },
    { token: "space.48", px: 48 },
    { token: "space.52", px: 52 },
    { token: "space.56", px: 56 },
    { token: "space.60", px: 60 },
    { token: "space.64", px: 64 },
    { token: "space.72", px: 72 },
  ],

  radius: [
    { token: "radius.2",       px: 2    },
    { token: "radius.4",       px: 4    },
    { token: "radius.6",       px: 6    },
    { token: "radius.8",       px: 8    },
    { token: "radius.10",      px: 10   },
    { token: "radius.12",      px: 12   },
    { token: "radius.14",      px: 14   },
    { token: "radius.16",      px: 16   },
    { token: "radius.18",      px: 18   },
    { token: "radius.20",      px: 20   },
    { token: "radius.24",      px: 24   },
    { token: "radius.28",      px: 28   },
    { token: "radius.32",      px: 32   },
    { token: "radius.36",      px: 36   },
    { token: "radius.40",      px: 40   },
    { token: "radius.rounded", px: 9999 },
  ],
} as const;
