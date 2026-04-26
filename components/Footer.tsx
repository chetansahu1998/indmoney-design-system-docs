export default function Footer() {
  const col1 = ["Get started", "Foundations", "Components", "Patterns"];
  const col2 = ["Resources", "Changelog", "GitHub", "Figma"];

  return (
    <footer
      style={{
        background: "var(--bg-surface)",
        borderTop: "1px solid var(--border)",
        padding: "64px 40px 40px",
        display: "grid",
        gridTemplateColumns: "1fr auto auto",
        gap: 48,
        alignItems: "start",
      }}
    >
      <div>
        <div style={{ fontSize: 32, fontWeight: 700, letterSpacing: "-0.8px", color: "var(--text-1)" }}>
          Field DS
        </div>
        <div style={{ fontSize: 13, color: "var(--text-3)", marginTop: 12 }}>
          © 2026 noon. All rights reserved.
        </div>
      </div>
      {[col1, col2].map((col, i) => (
        <div key={i} style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          {col.map((label) => (
            <a key={label} href="#" style={{ fontSize: 13, color: "var(--text-2)", textDecoration: "none" }}>
              {label}
            </a>
          ))}
        </div>
      ))}
    </footer>
  );
}
