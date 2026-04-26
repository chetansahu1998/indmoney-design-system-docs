"use client";
import { useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { useUIStore } from "@/lib/ui-store";
import { useAuth, login, triggerSync } from "@/lib/auth-client";
import { brandLabel, currentBrand } from "@/lib/brand";

/**
 * Sync now modal. Flow:
 *   1. Not authenticated → email + password form → POST /v1/auth/login
 *   2. Authenticated     → "Sync now" button → POST /api/sync (proxy → ds-service)
 *   3. Result            → status/trace surfaced inline; refresh after success
 *
 * Closed by Esc, click outside, or X. Persistent JWT keeps users logged in
 * across page reloads via Zustand persist.
 */

interface Props {
  open: boolean;
  onClose: () => void;
}

export default function SyncModal({ open, onClose }: Props) {
  const token = useAuth((s) => s.token);
  const email = useAuth((s) => s.email);
  const logout = useAuth((s) => s.logout);

  const [loginEmail, setLoginEmail] = useState("");
  const [loginPassword, setLoginPassword] = useState("");
  const [loginError, setLoginError] = useState<string | null>(null);
  const [loginPending, setLoginPending] = useState(false);

  const [syncPending, setSyncPending] = useState(false);
  const [syncResult, setSyncResult] = useState<
    | { kind: "ok"; status: string; traceId: string }
    | { kind: "err"; error: string }
    | null
  >(null);

  const brand = currentBrand();

  async function handleLogin(e: React.FormEvent) {
    e.preventDefault();
    setLoginError(null);
    setLoginPending(true);
    const result = await login(loginEmail, loginPassword);
    setLoginPending(false);
    if (!result.ok) setLoginError(result.error);
    setLoginPassword("");
  }

  async function handleSync() {
    setSyncPending(true);
    setSyncResult(null);
    const r = await triggerSync(brand);
    setSyncPending(false);
    if (r.ok) {
      setSyncResult({ kind: "ok", status: r.status, traceId: r.traceId });
    } else {
      setSyncResult({ kind: "err", error: r.error });
    }
  }

  return (
    <AnimatePresence>
      {open && (
        <motion.div
          key="overlay"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          transition={{ duration: 0.18 }}
          onClick={onClose}
          style={{
            position: "fixed",
            inset: 0,
            zIndex: 200,
            background: "rgba(0,0,0,0.55)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            backdropFilter: "blur(4px)",
          }}
        >
          <motion.div
            key="panel"
            initial={{ y: -16, opacity: 0, scale: 0.96 }}
            animate={{ y: 0, opacity: 1, scale: 1 }}
            exit={{ y: -16, opacity: 0, scale: 0.96 }}
            transition={{ type: "spring", stiffness: 320, damping: 28 }}
            onClick={(e) => e.stopPropagation()}
            style={{
              width: "min(440px, 92vw)",
              background: "var(--bg-surface)",
              border: "1px solid var(--border-strong)",
              borderRadius: 14,
              overflow: "hidden",
              boxShadow:
                "0 32px 80px rgba(0,0,0,0.45), 0 0 0 1px rgba(255,255,255,0.04)",
            }}
          >
            {/* Header */}
            <div
              style={{
                padding: "16px 20px",
                borderBottom: "1px solid var(--border)",
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
              }}
            >
              <div>
                <div style={{ fontSize: 15, fontWeight: 600, color: "var(--text-1)" }}>
                  Sync {brandLabel(brand)} tokens
                </div>
                {token && email && (
                  <div style={{ fontSize: 11, color: "var(--text-3)", marginTop: 2 }}>
                    Signed in as {email}
                  </div>
                )}
              </div>
              <button
                onClick={onClose}
                style={{
                  background: "var(--bg-surface-2)",
                  border: "1px solid var(--border)",
                  color: "var(--text-2)",
                  borderRadius: 6,
                  padding: "4px 8px",
                  fontFamily: "var(--font-mono)",
                  fontSize: 11,
                  cursor: "pointer",
                }}
              >
                esc
              </button>
            </div>

            {/* Body */}
            <div style={{ padding: 20 }}>
              {!token ? (
                <form onSubmit={handleLogin}>
                  <p style={{ fontSize: 13, color: "var(--text-2)", marginBottom: 16, lineHeight: 1.5 }}>
                    Sign in to ds-service to trigger a sync. Operator credentials
                    (created via bootstrap) are required.
                  </p>
                  <label
                    style={{ display: "block", fontSize: 12, color: "var(--text-3)", marginBottom: 6 }}
                  >
                    Email
                  </label>
                  <input
                    type="email"
                    value={loginEmail}
                    onChange={(e) => setLoginEmail(e.target.value)}
                    required
                    autoFocus
                    style={inputStyle}
                  />
                  <label
                    style={{ display: "block", fontSize: 12, color: "var(--text-3)", marginTop: 12, marginBottom: 6 }}
                  >
                    Password
                  </label>
                  <input
                    type="password"
                    value={loginPassword}
                    onChange={(e) => setLoginPassword(e.target.value)}
                    required
                    style={inputStyle}
                  />
                  {loginError && (
                    <div
                      style={{
                        marginTop: 12,
                        padding: "8px 10px",
                        background: "rgba(239, 68, 68, 0.1)",
                        border: "1px solid rgba(239, 68, 68, 0.3)",
                        borderRadius: 6,
                        fontSize: 12,
                        color: "#ef4444",
                      }}
                    >
                      {loginError}
                    </div>
                  )}
                  <button
                    type="submit"
                    disabled={loginPending}
                    style={{
                      ...primaryButtonStyle,
                      marginTop: 16,
                      width: "100%",
                      opacity: loginPending ? 0.6 : 1,
                    }}
                  >
                    {loginPending ? "Signing in…" : "Sign in"}
                  </button>
                </form>
              ) : (
                <div>
                  <p
                    style={{ fontSize: 13, color: "var(--text-2)", marginBottom: 14, lineHeight: 1.55 }}
                  >
                    Re-runs the Figma extraction for{" "}
                    <strong style={{ color: "var(--text-1)" }}>{brandLabel(brand)}</strong>,
                    computes the canonical hash, and writes updated tokens to{" "}
                    <code
                      style={{
                        fontFamily: "var(--font-mono)",
                        fontSize: 11,
                        background: "var(--bg-surface-2)",
                        padding: "1px 5px",
                        borderRadius: 4,
                      }}
                    >
                      lib/tokens/{brand}/
                    </code>
                    . If nothing changed, the sync is skipped.
                  </p>

                  {syncResult?.kind === "ok" && (
                    <div
                      style={{
                        padding: 12,
                        background: "rgba(34, 197, 94, 0.08)",
                        border: "1px solid rgba(34, 197, 94, 0.3)",
                        borderRadius: 6,
                        marginBottom: 14,
                      }}
                    >
                      <div style={{ fontSize: 13, fontWeight: 600, color: "#22c55e" }}>
                        Sync {syncResult.status === "noop" ? "complete (no changes)" : "queued"}
                      </div>
                      <div
                        style={{
                          fontSize: 10,
                          fontFamily: "var(--font-mono)",
                          color: "var(--text-3)",
                          marginTop: 2,
                        }}
                      >
                        trace: {syncResult.traceId}
                      </div>
                      {syncResult.status !== "noop" && (
                        <div style={{ fontSize: 11, color: "var(--text-2)", marginTop: 6 }}>
                          Reload the page to see updated tokens.
                        </div>
                      )}
                    </div>
                  )}
                  {syncResult?.kind === "err" && (
                    <div
                      style={{
                        padding: 12,
                        background: "rgba(239, 68, 68, 0.08)",
                        border: "1px solid rgba(239, 68, 68, 0.3)",
                        borderRadius: 6,
                        marginBottom: 14,
                        fontSize: 12,
                        color: "#ef4444",
                      }}
                    >
                      Sync failed: {syncResult.error}
                    </div>
                  )}

                  <div style={{ display: "flex", gap: 8 }}>
                    <button
                      onClick={handleSync}
                      disabled={syncPending}
                      style={{
                        ...primaryButtonStyle,
                        flex: 1,
                        opacity: syncPending ? 0.6 : 1,
                      }}
                    >
                      {syncPending ? "Syncing…" : "Sync now"}
                    </button>
                    <button
                      onClick={() => {
                        logout();
                        setSyncResult(null);
                      }}
                      style={{
                        padding: "9px 14px",
                        background: "var(--bg-surface-2)",
                        border: "1px solid var(--border)",
                        color: "var(--text-2)",
                        borderRadius: 7,
                        fontSize: 12,
                        cursor: "pointer",
                      }}
                    >
                      Sign out
                    </button>
                  </div>
                </div>
              )}
            </div>
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}

const inputStyle: React.CSSProperties = {
  width: "100%",
  padding: "8px 10px",
  background: "var(--bg-surface-2)",
  border: "1px solid var(--border)",
  borderRadius: 6,
  fontSize: 13,
  color: "var(--text-1)",
  fontFamily: "var(--font-sans)",
  outline: "none",
};

const primaryButtonStyle: React.CSSProperties = {
  padding: "9px 14px",
  background: "var(--accent)",
  border: "1px solid var(--accent)",
  color: "#fff",
  borderRadius: 7,
  fontSize: 13,
  fontWeight: 600,
  cursor: "pointer",
};
