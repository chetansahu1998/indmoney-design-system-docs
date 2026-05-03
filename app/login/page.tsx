"use client";

import { Suspense, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";

import { login, useAuth } from "@/lib/auth-client";

function LoginForm() {
  const router = useRouter();
  const params = useSearchParams();
  const next = params.get("next") || "/inbox";
  const token = useAuth((s) => s.token);

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (token) router.replace(next);
  }, [token, next, router]);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    const res = await login(email, password);
    setSubmitting(false);
    if (!res.ok) {
      setError(res.error);
      return;
    }
    router.replace(next);
  }

  return (
    <main className="login-shell">
      <div className="login-card">
        <header>
          <h1>Sign in</h1>
          <p>Use your INDmoney design-system credentials.</p>
        </header>

        <form onSubmit={onSubmit} noValidate>
          <label>
            <span>Email</span>
            <input
              type="email"
              autoComplete="username"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
              autoFocus
            />
          </label>

          <label>
            <span>Password</span>
            <input
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
            />
          </label>

          {error && <p className="err" role="alert">{error}</p>}

          <button type="submit" disabled={submitting || !email || !password}>
            {submitting ? "Signing in…" : "Sign in"}
          </button>
        </form>

        <p className="hint">
          After signing in you'll be returned to <code>{next}</code>.
        </p>
      </div>

      <style jsx>{`
        .login-shell {
          min-height: 100vh;
          display: grid;
          place-items: center;
          padding: 24px;
          background: var(--bg-canvas, #050810);
          color: var(--fg, #e6ebf5);
        }
        .login-card {
          width: 100%;
          max-width: 380px;
          padding: 32px;
          border-radius: 16px;
          background: var(--bg-elevated, rgba(255, 255, 255, 0.04));
          border: 1px solid var(--border, rgba(255, 255, 255, 0.08));
          box-shadow: 0 20px 60px rgba(0, 0, 0, 0.4);
        }
        header {
          margin-bottom: 24px;
        }
        h1 {
          margin: 0 0 8px;
          font-size: 22px;
          font-weight: 600;
          letter-spacing: -0.01em;
        }
        p {
          margin: 0;
          font-size: 14px;
          color: var(--fg-muted, rgba(255, 255, 255, 0.6));
        }
        form {
          display: grid;
          gap: 14px;
        }
        label {
          display: grid;
          gap: 6px;
        }
        label span {
          font-size: 12px;
          font-weight: 500;
          color: var(--fg-muted, rgba(255, 255, 255, 0.7));
          letter-spacing: 0.02em;
          text-transform: uppercase;
        }
        input {
          height: 40px;
          padding: 0 12px;
          font: inherit;
          color: inherit;
          background: var(--bg-input, rgba(0, 0, 0, 0.3));
          border: 1px solid var(--border, rgba(255, 255, 255, 0.12));
          border-radius: 8px;
          outline: none;
          transition: border-color 120ms;
        }
        input:focus {
          border-color: var(--accent, #7b9fff);
        }
        button {
          height: 40px;
          margin-top: 8px;
          font: inherit;
          font-weight: 600;
          color: var(--accent-fg, #fff);
          background: var(--accent, #7b9fff);
          border: 0;
          border-radius: 8px;
          cursor: pointer;
          transition: opacity 120ms;
        }
        button:disabled {
          opacity: 0.5;
          cursor: not-allowed;
        }
        .err {
          padding: 8px 12px;
          font-size: 13px;
          color: #ff6b6b;
          background: rgba(255, 107, 107, 0.08);
          border: 1px solid rgba(255, 107, 107, 0.2);
          border-radius: 6px;
        }
        .hint {
          margin-top: 16px;
          font-size: 12px;
          color: var(--fg-muted, rgba(255, 255, 255, 0.5));
        }
        code {
          padding: 1px 5px;
          font-family: var(--font-mono, ui-monospace, monospace);
          font-size: 11px;
          background: rgba(255, 255, 255, 0.06);
          border-radius: 3px;
        }
      `}</style>
    </main>
  );
}

export default function LoginPage() {
  return (
    <Suspense fallback={<div style={{ minHeight: "100vh", background: "var(--bg-canvas, #050810)" }} />}>
      <LoginForm />
    </Suspense>
  );
}
