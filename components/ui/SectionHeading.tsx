"use client";
import { motion } from "framer-motion";
import { Separator } from "@/components/ui/separator";

export default function SectionHeading({ id, title }: { id: string; title: string }) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 12 }}
      whileInView={{ opacity: 1, y: 0 }}
      viewport={{ once: true, margin: "-40px" }}
      transition={{ duration: 0.4, ease: [0.33, 1, 0.68, 1] }}
      style={{ marginBottom: 32 }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 12, paddingBottom: 16 }}>
        <h2 style={{ fontSize: 28, fontWeight: 700, letterSpacing: "-0.6px", color: "var(--text-1)" }}>
          {title}
        </h2>
        <motion.button
          onClick={() => navigator.clipboard.writeText(window.location.href.split("#")[0] + "#" + id).catch(() => {})}
          title="Copy link"
          whileHover={{ scale: 1.12, color: "var(--accent)" }}
          whileTap={{ scale: 0.9 }}
          transition={{ type: "spring", stiffness: 300, damping: 22 }}
          style={{
            marginLeft: "auto", width: 32, height: 32, borderRadius: 6,
            background: "transparent", border: "none", cursor: "pointer",
            display: "flex", alignItems: "center", justifyContent: "center",
            color: "var(--text-3)",
          }}
        >
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path d="M6.5 9.5L9.5 6.5M7 4.5l1.5-1.5a3 3 0 014.243 4.243L11 9M9 11.5l-1.5 1.5a3 3 0 01-4.243-4.243L5 7"
              stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
          </svg>
        </motion.button>
      </div>
      <Separator style={{ background: "var(--border)" }} />
    </motion.div>
  );
}
