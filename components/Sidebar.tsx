"use client";
import { useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { useIsMobile } from "@/lib/use-mobile";

const nav = [
  { label: "Logo",         sub: [] },
  {
    label: "Typography",
    sub: [
      { label: "Type ramp",      href: "#type-ramp" },
      { label: "Type hierarchy", href: "#type-hierarchy" },
      { label: "Type styles",    href: "#type-styles" },
    ],
  },
  {
    label: "Color",
    sub: [
      { label: "Base palette",   href: "#color" },
      { label: "Text & icon",    href: "#color-text" },
      { label: "Surface",        href: "#color-surface" },
      { label: "Border",         href: "#color-border" },
    ],
  },
  {
    label: "Spacing",
    sub: [
      { label: "Scale",  href: "#spacing-scale" },
      { label: "Radius", href: "#spacing-radius" },
    ],
  },
  { label: "Iconography",  sub: [
      { label: "System icons", href: "#iconography" },
    ],
  },
  {
    label: "Motion",
    sub: [
      { label: "Spring presets", href: "#motion-spring" },
      { label: "Opacity",        href: "#motion-opacity" },
      { label: "Scale / Press",  href: "#motion-scale" },
    ],
  },
  { label: "Accessibility", sub: [] },
];

function NavTree({
  activeSection,
  onNavigate,
}: {
  activeSection: string;
  onNavigate?: () => void;
}) {
  const [open, setOpen] = useState<Record<string, boolean>>({
    Typography: true, Color: true, Spacing: true, Motion: true, Iconography: true,
  });

  return (
    <>
      <div style={{ fontSize: 13, fontWeight: 600, color: "var(--text-1)", padding: "10px 16px 6px" }}>
        Foundations
      </div>

      {nav.map((item) => (
        <div key={item.label}>
          <motion.button
            onClick={() => item.sub.length && setOpen((o) => ({ ...o, [item.label]: !o[item.label] }))}
            whileHover={item.sub.length ? { x: 1 } : {}}
            transition={{ type: "spring", stiffness: 300, damping: 26 }}
            style={{
              display: "flex", alignItems: "center", justifyContent: "space-between",
              width: "calc(100% - 12px)",
              padding: "10px 16px", margin: "1px 6px",
              fontSize: 14, fontWeight: item.sub.length ? 500 : 400,
              color: open[item.label] ? "var(--text-1)" : "var(--text-2)",
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
                animate={{ rotate: open[item.label] ? 180 : 0 }}
                transition={{ type: "spring", stiffness: 300, damping: 26 }}
                style={{ color: "var(--text-3)" }}
              >
                <path d="M3 5l4 4 4-4" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
              </motion.svg>
            )}
          </motion.button>

          <AnimatePresence initial={false}>
            {open[item.label] && item.sub.length > 0 && (
              <motion.div
                initial={{ height: 0, opacity: 0 }}
                animate={{ height: "auto", opacity: 1 }}
                exit={{ height: 0, opacity: 0 }}
                transition={{ duration: 0.22, ease: [0.33, 1, 0.68, 1] }}
                style={{ overflow: "hidden" }}
              >
                {item.sub.map((s) => {
                  const isActive = activeSection === s.href.slice(1);
                  return (
                    <div key={s.href} style={{ position: "relative", margin: "1px 6px" }}>
                      {isActive && (
                        <motion.div
                          layoutId="sidebar-active"
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
    </>
  );
}

/* ── Desktop sidebar ── */
export function DesktopSidebar({ activeSection }: { activeSection: string }) {
  return (
    <nav
      className="sidebar-desktop"
      style={{
        background: "var(--bg-page)",
        borderRight: "1px solid var(--border)",
      }}
    >
      <ScrollArea style={{ height: "100%", padding: "24px 0 48px" }}>
        <NavTree activeSection={activeSection} />
      </ScrollArea>
    </nav>
  );
}

/* ── Mobile drawer ── */
export function MobileDrawer({
  open,
  onClose,
  activeSection,
}: {
  open: boolean;
  onClose: () => void;
  activeSection: string;
}) {
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
            Field DS
          </SheetTitle>
        </SheetHeader>
        <ScrollArea style={{ height: "calc(100% - 64px)" }}>
          <NavTree activeSection={activeSection} onNavigate={onClose} />
        </ScrollArea>
      </SheetContent>
    </Sheet>
  );
}

/* ── Default export: auto-switches ── */
export default function Sidebar({
  activeSection,
  mobileOpen,
  onMobileClose,
}: {
  activeSection: string;
  mobileOpen?: boolean;
  onMobileClose?: () => void;
}) {
  const isMobile = useIsMobile();

  if (isMobile) {
    return (
      <MobileDrawer
        open={mobileOpen ?? false}
        onClose={onMobileClose ?? (() => {})}
        activeSection={activeSection}
      />
    );
  }

  return <DesktopSidebar activeSection={activeSection} />;
}
