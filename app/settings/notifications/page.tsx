"use client";

/**
 * Phase 7.5 — notification preference center.
 *
 * Per-user form for the two channels Phase 5 ships: Slack webhook + email.
 * Cadence options: off / daily / weekly. Saves are PUT to
 * /v1/users/me/notification-preferences keyed by channel.
 */

import { useEffect, useState } from "react";

import { useAuth } from "@/lib/auth-client";
import PageShell from "@/components/PageShell";

import { adminFetchJSON } from "../../atlas/admin/_lib/adminFetch";

interface PrefRecord {
  user_id: string;
  channel: "slack" | "email";
  cadence: "off" | "daily" | "weekly";
  slack_webhook_url?: string;
  email_address?: string;
  user_tz?: string;
  last_digest_at?: string;
  updated_at?: string;
}

interface PrefsView {
  slack: PrefRecord;
  email: PrefRecord;
}

const CADENCE_OPTIONS: PrefRecord["cadence"][] = ["off", "daily", "weekly"];

export default function NotificationPrefsPage() {
  const token = useAuth((s) => s.token);
  const [view, setView] = useState<PrefsView | null>(null);
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState<string | null>(null);
  const [savingChannel, setSavingChannel] = useState<string | null>(null);
  const [savedChannel, setSavedChannel] = useState<string | null>(null);

  async function load() {
    setStatus("loading");
    try {
      const body = await adminFetchJSON<PrefsView>(
        "/v1/users/me/notification-preferences",
      );
      setView(body);
      setStatus("ready");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setStatus("error");
    }
  }

  useEffect(() => {
    if (!token) return;
    void load();
  }, [token]);

  async function save(channel: PrefRecord["channel"], patch: Partial<PrefRecord>) {
    if (!view) return;
    const current = channel === "slack" ? view.slack : view.email;
    const merged: PrefRecord = { ...current, ...patch };
    setSavingChannel(channel);
    setSavedChannel(null);
    try {
      const updated = await adminFetchJSON<PrefRecord>(
        "/v1/users/me/notification-preferences",
        {
          method: "PUT",
          body: {
            channel: merged.channel,
            cadence: merged.cadence,
            slack_webhook_url: merged.slack_webhook_url ?? "",
            email_address: merged.email_address ?? "",
            user_tz: merged.user_tz ?? "",
          },
        },
      );
      setView({
        ...view,
        [channel]: updated,
      });
      setSavedChannel(channel);
      window.setTimeout(() => setSavedChannel((c) => (c === channel ? null : c)), 1500);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSavingChannel(null);
    }
  }

  if (!token) {
    return (
      <PageShell>
        <main className="page">
          <div className="card">
            <h1>Sign in to manage notifications</h1>
            <p>You need to be signed in to view your preferences.</p>
            <p style={{ marginTop: 16 }}>
              <a
                href="/login?next=/settings/notifications"
                style={{
                  display: "inline-block",
                  padding: "8px 16px",
                  background: "var(--accent)",
                  color: "#fff",
                  borderRadius: 8,
                  fontWeight: 600,
                  textDecoration: "none",
                }}
              >
                Sign in
              </a>
            </p>
          </div>
          <PageStyles />
        </main>
      </PageShell>
    );
  }

  return (
    <PageShell>
    <main className="page">
      <header>
        <h1>Notifications</h1>
        <p>
          Choose how you want to be notified about decisions, mentions, and
          violations on flows you own. The in-app inbox is always on; these
          settings control the off-platform digests.
        </p>
      </header>

      {status === "loading" && <p className="msg">Loading preferences…</p>}
      {status === "error" && (
        <p className="msg err">
          Couldn&apos;t load: {error}.{" "}
          <button onClick={() => void load()}>Retry</button>
        </p>
      )}
      {status === "ready" && view && (
        <div className="cards">
          <ChannelCard
            title="Slack"
            description="A digest is posted to your Slack webhook URL on the chosen cadence."
            record={view.slack}
            saving={savingChannel === "slack"}
            saved={savedChannel === "slack"}
            onSave={(patch) => save("slack", patch)}
          >
            <label className="field">
              <span>Webhook URL</span>
              <input
                type="url"
                placeholder="https://hooks.slack.com/services/…"
                defaultValue={view.slack.slack_webhook_url ?? ""}
                onBlur={(e) => {
                  const v = e.target.value.trim();
                  if (v !== (view.slack.slack_webhook_url ?? "")) {
                    void save("slack", { slack_webhook_url: v });
                  }
                }}
              />
            </label>
          </ChannelCard>

          <ChannelCard
            title="Email"
            description="Same digest content, delivered to your email address. SMTP must be configured server-side."
            record={view.email}
            saving={savingChannel === "email"}
            saved={savedChannel === "email"}
            onSave={(patch) => save("email", patch)}
          >
            <label className="field">
              <span>Email address</span>
              <input
                type="email"
                placeholder="you@indmoney.com"
                defaultValue={view.email.email_address ?? ""}
                onBlur={(e) => {
                  const v = e.target.value.trim();
                  if (v !== (view.email.email_address ?? "")) {
                    void save("email", { email_address: v });
                  }
                }}
              />
            </label>
          </ChannelCard>
        </div>
      )}
      <PageStyles />
    </main>
    </PageShell>
  );
}

interface ChannelCardProps {
  title: string;
  description: string;
  record: PrefRecord;
  saving: boolean;
  saved: boolean;
  onSave: (patch: Partial<PrefRecord>) => void;
  children: React.ReactNode;
}

function ChannelCard({
  title,
  description,
  record,
  saving,
  saved,
  onSave,
  children,
}: ChannelCardProps) {
  return (
    <section className="card">
      <header>
        <h2>{title}</h2>
        <span className="status" aria-live="polite">
          {saving ? "Saving…" : saved ? "Saved" : ""}
        </span>
      </header>
      <p className="desc">{description}</p>

      <label className="field">
        <span>Cadence</span>
        <div className="seg">
          {CADENCE_OPTIONS.map((c) => (
            <button
              key={c}
              type="button"
              className={record.cadence === c ? "active" : ""}
              onClick={() => onSave({ cadence: c })}
              disabled={saving}
            >
              {c === "off" ? "Off" : c === "daily" ? "Daily" : "Weekly"}
            </button>
          ))}
        </div>
      </label>

      {children}

      <label className="field">
        <span>Time zone (IANA, e.g. Asia/Kolkata)</span>
        <input
          type="text"
          placeholder="Asia/Kolkata"
          defaultValue={record.user_tz ?? ""}
          onBlur={(e) => {
            const v = e.target.value.trim();
            if (v !== (record.user_tz ?? "")) onSave({ user_tz: v });
          }}
        />
      </label>

      <style jsx>{`
        .card {
          padding: 24px;
          border: 1px solid var(--border);
          border-radius: 12px;
          background: var(--bg-surface);
          display: flex;
          flex-direction: column;
          gap: 14px;
        }
        .card header {
          display: flex;
          justify-content: space-between;
          align-items: baseline;
        }
        .card h2 {
          margin: 0;
          font-size: 16px;
          font-weight: 600;
        }
        .status {
          font-size: 11px;
          color: var(--text-3);
        }
        .desc {
          margin: 0;
          color: var(--text-3);
          font-size: 12px;
        }
        .field {
          display: flex;
          flex-direction: column;
          gap: 6px;
        }
        .field span {
          font-size: 11px;
          color: var(--text-3);
          letter-spacing: 0.04em;
          text-transform: uppercase;
        }
        .field input {
          padding: 8px 12px;
          background: var(--bg-canvas);
          border: 1px solid var(--border);
          border-radius: 8px;
          color: var(--text-1);
          font-size: 13px;
          font-family: inherit;
        }
        .seg {
          display: inline-flex;
          gap: 4px;
          padding: 4px;
          background: var(--bg-canvas);
          border: 1px solid var(--border);
          border-radius: 999px;
          width: fit-content;
        }
        .seg button {
          padding: 6px 16px;
          background: transparent;
          border: none;
          border-radius: 999px;
          color: var(--text-3);
          font-size: 12px;
          font-weight: 500;
          cursor: pointer;
        }
        .seg button.active {
          background: var(--accent);
          color: var(--bg-canvas);
        }
        .seg button:disabled {
          opacity: 0.5;
        }
      `}</style>
    </section>
  );
}

function PageStyles() {
  return (
    <style jsx global>{`
      .page {
        min-height: 100vh;
        background: var(--bg-canvas);
        color: var(--text-1);
        font-family: var(--font-sans, "Inter Variable", sans-serif);
        padding: 32px 32px 64px;
        max-width: 720px;
        margin: 0 auto;
      }
      .page > header h1 {
        margin: 0 0 8px;
        font-size: 28px;
        font-weight: 600;
      }
      .page > header p {
        margin: 0 0 32px;
        color: var(--text-3);
        line-height: 1.6;
      }
      .msg {
        color: var(--text-3);
      }
      .msg.err {
        color: #ffb347;
      }
      .msg.err button {
        margin-left: 8px;
        padding: 4px 10px;
        border: 1px solid var(--border);
        border-radius: 6px;
        background: transparent;
        color: inherit;
        cursor: pointer;
      }
      .cards {
        display: flex;
        flex-direction: column;
        gap: 16px;
      }
    `}</style>
  );
}
