"use client";
import { useEffect, useRef } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { motion, AnimatePresence, LayoutGroup } from "framer-motion";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { useIsMobile } from "@/lib/use-mobile";
import { useUIStore } from "@/lib/ui-store";
import { brandLabel, currentBrand } from "@/lib/brand";

/** Top-level routes — same list as Header.PageNav. Mirrored into the mobile
 *  drawer because globals.css hides .page-nav on mobile (the inline strip
 *  is too cramped). Keeping these in sync manually for now; could become a
 *  shared const later. */
const TOP_ROUTES = [
  { href: "/",              label: "Foundations" },
  { href: "/atlas",         label: "Atlas" },
  { href: "/projects",      label: "Projects" },
  { href: "/components",    label: "Components" },
  { href: "/icons",         label: "Icons" },
  { href: "/illustrations", label: "Illustrations" },
  { href: "/logos",         label: "Logos" },
  { href: "/inbox",         label: "Inbox" },
  { href: "/files",         label: "Files" },
  { href: "/health",        label: "Health" },
];

/* ── Types ─────────────────────────────────────────────────────────────── */

export interface NavSubItem {
  label: string;
  href: string;
}

export interface NavGroup {
  label: string;
  /** Group is expanded by default. Lazy users get the most-used groups
   *  pre-opened; rare ones stay collapsed. */
  defaultOpen?: boolean;
  sub: NavSubItem[];
}

/* ── Foundations default nav ──────────────────────────────────────────────
 * Anchors must match section IDs rendered by components/sections/*.
 * Color buckets are derived from semantic.tokens.json keys; keep this list
 * in sync when the Glyph extraction surfaces new buckets. */
export const FOUNDATIONS_NAV: NavGroup[] = [
  {
    label: "Color",
    defaultOpen: true,
    sub: [
      { label: "Surface",         href: "#color-surface" },
      { label: "Text & icon",     href: "#color-text-n-icon" },
      { label: "Tertiary",        href: "#color-tertiary" },
      { label: "Market ticker",   href: "#color-surface-market-ticker" },
      { label: "Special",         href: "#color-special" },
      { label: "Base palette",    href: "#color-base" },
    ],
  },
  {
    label: "Typography",
    defaultOpen: true,
    sub: [
      { label: "Headings",   href: "#type-heading" },
      { label: "Subtitles",  href: "#type-subtitle" },
      { label: "Body",       href: "#type-body" },
      { label: "Caption",    href: "#type-caption" },
      { label: "Overline",   href: "#type-overline" },
      { label: "Small",      href: "#type-small" },
    ],
  },
  {
    label: "Spacing",
    sub: [
      { label: "Scale",   href: "#spacing-scale" },
      { label: "Padding", href: "#spacing-padding" },
      { label: "Radius",  href: "#spacing-radius" },
    ],
  },
  {
    label: "Effects",
    sub: [{ label: "Shadows", href: "#effects" }],
  },
];

/* ── NavTree (the actual list) ───────────────────────────────────────── */

interface NavTreeProps {
  nav: NavGroup[];
  title: string;
  activeSection: string;
  onNavigate?: () => void;
  /** Unique LayoutGroup id so the desktop pill and mobile-drawer pill don't
   *  share a layoutId — when both mount, framer would otherwise animate one
   *  pill across the screen between the two NavTree instances. */
  layoutScope: string;
}

function NavTree({ nav, title, activeSection, onNavigate, layoutScope }: NavTreeProps) {
  // Group expand/collapse state lives in the UI store so it survives route
  // changes (the previous local-useState reset every time you switched
  // shells, e.g. / → /icons → / re-expanded a manually-collapsed group).
  // groupKey = `${layoutScope}:${label}` so the desktop and mobile drawer
  // can hold independent state — collapsing on mobile shouldn't collapse
  // on desktop and vice versa.
  const collapsedGroups = useUIStore((s) => s.collapsedGroups);
  const toggleGroup = useUIStore((s) => s.toggleGroup);
  const groupKey = (label: string) => `${layoutScope}:${label}`;
  const isOpen = (g: NavGroup) => {
    const key = groupKey(g.label);
    if (collapsedGroups.has(key)) return false;
    if (collapsedGroups.has(`${key}:explicit-open`)) return true;
    return g.defaultOpen !== false;
  };
  const toggleOpen = (g: NavGroup) => {
    const key = groupKey(g.label);
    const explicit = `${key}:explicit-open`;
    // Default-closed groups need a positive flag to stay open across mounts;
    // default-open groups only need a negative flag to stay closed.
    if (g.defaultOpen === false) {
      // Toggle the explicit-open flag.
      toggleGroup(explicit);
    } else {
      toggleGroup(key);
    }
  };

  // Auto-scroll the sidebar so the active item stays in view. Without this,
  // long sidebars (e.g. /files with 13 product files + sub-anchors) leave
  // the active pill hidden inside the ScrollArea — designer scrolls main
  // content but the sidebar pill scrolls offscreen.
  const treeRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!activeSection) return;
    const root = treeRef.current;
    if (!root) return;
    const target = root.querySelector(`[data-anchor-id="${activeSection}"]`);
    if (target && "scrollIntoView" in target) {
      (target as HTMLElement).scrollIntoView({ block: "nearest", behavior: "smooth" });
    }
  }, [activeSection]);

  return (
    <LayoutGroup id={layoutScope}>
      <div ref={treeRef} style={{ fontSize: 13, fontWeight: 600, color: "var(--text-1)", padding: "10px 16px 6px" }}>
        {title}
      </div>

      {nav.map((item) => (
        <div key={item.label}>
          <motion.button
            onClick={() => item.sub.length && toggleOpen(item)}
            whileHover={item.sub.length ? { x: 1 } : {}}
            transition={{ type: "spring", stiffness: 300, damping: 26 }}
            style={{
              display: "flex", alignItems: "center", justifyContent: "space-between",
              width: "calc(100% - 12px)",
              padding: "10px 16px", margin: "1px 6px",
              fontSize: 14, fontWeight: item.sub.length ? 500 : 400,
              color: isOpen(item) ? "var(--text-1)" : "var(--text-2)",
              background: "none", border: "none",
              cursor: item.sub.length ? "pointer" : "default",
              borderRadius: 8, textAlign: "left",
              transition: "color 0.15s",
            }}
          >
            <span>{item.label}</span>
            {item.sub.length > 0 && (
              <motion.svg
                width="14" height="14" viewBox="0 0 14 14" fill="none"
                animate={{ rotate: isOpen(item) ? 180 : 0 }}
                transition={{ type: "spring", stiffness: 300, damping: 26 }}
                style={{ color: "var(--text-3)" }}
              >
                <path d="M3 5l4 4 4-4" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
              </motion.svg>
            )}
          </motion.button>

          <AnimatePresence initial={false}>
            {isOpen(item) && item.sub.length > 0 && (
              <motion.div
                initial={{ height: 0, opacity: 0 }}
                animate={{ height: "auto", opacity: 1 }}
                exit={{ height: 0, opacity: 0 }}
                transition={{ duration: 0.22, ease: [0.33, 1, 0.68, 1] }}
                style={{ overflow: "hidden" }}
              >
                {item.sub.map((s) => {
                  const anchorId = s.href.startsWith("#") ? s.href.slice(1) : s.href;
                  const isActive = activeSection === anchorId;
                  return (
                    <div key={s.href} data-anchor-id={anchorId} style={{ position: "relative", margin: "1px 6px" }}>
                      {isActive && (
                        <motion.div
                          layoutId={`${layoutScope}-active`}
                          style={{
                            position: "absolute", inset: 0,
                            background: "var(--bg-surface)", borderRadius: 8,
                          }}
                          transition={{ type: "spring", stiffness: 300, damping: 26 }}
                        />
                      )}
                      <motion.a
                        href={s.href}
                        onClick={onNavigate}
                        whileHover={{ x: 2 }}
                        transition={{ type: "spring", stiffness: 300, damping: 26 }}
                        aria-current={isActive ? "true" : undefined}
                        style={{
                          position: "relative",
                          display: "block",
                          padding: "9px 16px 9px 34px",
                          fontSize: 14,
                          fontWeight: isActive ? 500 : 400,
                          color: isActive ? "var(--text-1)" : "var(--text-2)",
                          borderRadius: 8, textDecoration: "none",
                          transition: "color 0.15s",
                        }}
                      >
                        {s.label}
                      </motion.a>
                    </div>
                  );
                })}
              </motion.div>
            )}
          </AnimatePresence>
        </div>
      ))}
    </LayoutGroup>
  );
}

/* ── Desktop + mobile shells ────────────────────────────────────────── */

export function DesktopSidebar({
  nav,
  title,
  activeSection,
}: {
  nav: NavGroup[];
  title: string;
  activeSection: string;
}) {
  return (
    <nav
      className="sidebar-desktop"
      style={{
        background: "var(--bg-page)",
        borderRight: "1px solid var(--border)",
      }}
    >
      <ScrollArea style={{ height: "100%", padding: "24px 0 48px" }}>
        <NavTree
          nav={nav}
          title={title}
          activeSection={activeSection}
          layoutScope="sidebar-desktop"
        />
      </ScrollArea>
    </nav>
  );
}

export function MobileDrawer({
  nav,
  title,
  open,
  onClose,
  activeSection,
}: {
  nav: NavGroup[];
  title: string;
  open: boolean;
  onClose: () => void;
  activeSection: string;
}) {
  const brand = currentBrand();
  const pathname = usePathname() ?? "/";
  return (
    <Sheet open={open} onOpenChange={(v) => !v && onClose()}>
      <SheetContent
        side="left"
        style={{
          width: 280, padding: 0,
          background: "var(--bg-page)", border: "none",
          borderRight: "1px solid var(--border)",
        }}
      >
        <SheetHeader style={{ padding: "20px 16px 8px" }}>
          <SheetTitle style={{ fontSize: 16, fontWeight: 700, color: "var(--text-1)", textAlign: "left" }}>
            {brandLabel(brand)} DS
          </SheetTitle>
        </SheetHeader>

        {/* Top-route list — mirrors Header.PageNav since the inline desktop
         *  strip is hidden on mobile. Designers expect to switch between
         *  Foundations / Icons / Components / etc from the drawer. */}
        <div style={{ padding: "4px 8px 8px", borderBottom: "1px solid var(--border)" }}>
          <div
            style={{
              fontSize: 11,
              fontWeight: 600,
              color: "var(--text-3)",
              textTransform: "uppercase",
              letterSpacing: "0.06em",
              padding: "8px 12px 6px",
            }}
          >
            Sections
          </div>
          {TOP_ROUTES.map((r) => {
            const active = r.href === "/" ? pathname === "/" : pathname.startsWith(r.href);
            return (
              <Link
                key={r.href}
                href={r.href}
                onClick={onClose}
                aria-current={active ? "page" : undefined}
                style={{
                  display: "block",
                  padding: "10px 12px",
                  margin: "1px 0",
                  fontSize: 14,
                  fontWeight: active ? 600 : 500,
                  color: active ? "var(--text-1)" : "var(--text-2)",
                  background: active ? "var(--bg-surface-2)" : "transparent",
                  borderRadius: 8,
                  textDecoration: "none",
                }}
              >
                {r.label}
              </Link>
            );
          })}
        </div>

        <ScrollArea style={{ height: "calc(100% - 64px - 320px)" }}>
          <NavTree
            nav={nav}
            title={title}
            activeSection={activeSection}
            onNavigate={onClose}
            layoutScope="sidebar-mobile"
          />
        </ScrollArea>
      </SheetContent>
    </Sheet>
  );
}

export default function Sidebar({
  nav = FOUNDATIONS_NAV,
  title = "Foundations",
  activeSection,
  mobileOpen,
  onMobileClose,
}: {
  nav?: NavGroup[];
  title?: string;
  activeSection: string;
  mobileOpen?: boolean;
  onMobileClose?: () => void;
}) {
  const isMobile = useIsMobile();
  if (isMobile) {
    return (
      <MobileDrawer
        nav={nav}
        title={title}
        open={mobileOpen ?? false}
        onClose={onMobileClose ?? (() => {})}
        activeSection={activeSection}
      />
    );
  }
  return <DesktopSidebar nav={nav} title={title} activeSection={activeSection} />;
}
