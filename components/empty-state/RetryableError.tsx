"use client";

/**
 * RetryableError — Phase 3 U5 — wraps `EmptyState` variant=error with
 * exponential-backoff retry semantics.
 *
 * Behavior (per Phase 3 plan "Network-error recovery patterns"):
 *   1st failure → "Try again" button visible immediately.
 *   Click → calls `onRetry()`, button disabled, status flips to retrying.
 *   On retry failure → backoff to 2s, then 4s, then "Try again" link only
 *   (no auto-retry past 3 attempts; user must explicitly click).
 *
 * The wrapper does NOT own the network call — the parent passes
 * `onRetry`. The wrapper just orchestrates the backoff timing + button
 * state. Parent decides what success looks like (typically: it unmounts
 * the RetryableError once data arrives).
 *
 * Accessibility: button uses `aria-busy` while retrying so screen readers
 * announce the in-flight state.
 */

import { useCallback, useEffect, useRef, useState } from "react";
import EmptyState from "./EmptyState";

interface Props {
  /** Title override; defaults to EmptyState's "Something went wrong". */
  title?: string;
  /** Optional context — error code, trace_id, "we couldn't load
   *  violations", etc. Folded into the description copy. */
  detail?: string;
  /** Caller's retry handler. Throws or returns a rejected Promise = the
   *  retry failed and the wrapper backs off; resolves cleanly = success
   *  (the wrapper resets attempt count and lets the consumer unmount). */
  onRetry: () => void | Promise<void>;
  /** When true, render a network-offline variant instead of generic
   *  error (different sigil + copy). */
  offline?: boolean;
}

const BACKOFF_MS = [1_000, 2_000, 4_000] as const;
const MAX_ATTEMPTS = BACKOFF_MS.length;

export default function RetryableError({
  title,
  detail,
  onRetry,
  offline = false,
}: Props) {
  const [attempt, setAttempt] = useState(0);
  const [retrying, setRetrying] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Cancel any pending auto-backoff on unmount.
  useEffect(() => {
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, []);

  const doRetry = useCallback(async () => {
    setRetrying(true);
    setError(null);
    try {
      await onRetry();
      // Caller is responsible for unmounting on success; we just reset
      // local state in case they don't.
      setAttempt(0);
    } catch (err) {
      const next = attempt + 1;
      setAttempt(next);
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setRetrying(false);
    }
  }, [attempt, onRetry]);

  // Auto-retry with backoff for the first MAX_ATTEMPTS-1 failures.
  // After that, user must click manually.
  const showAutoRetry = attempt > 0 && attempt < MAX_ATTEMPTS && !retrying;

  useEffect(() => {
    if (!showAutoRetry) return;
    const delay = BACKOFF_MS[attempt - 1];
    timerRef.current = setTimeout(() => {
      void doRetry();
    }, delay);
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [showAutoRetry, attempt, doRetry]);

  const description = (() => {
    if (retrying) return "Retrying…";
    if (showAutoRetry) {
      const seconds = Math.round(BACKOFF_MS[attempt - 1] / 1_000);
      return `Retrying in ${seconds}s${error ? ` (last error: ${error})` : ""}.`;
    }
    if (attempt >= MAX_ATTEMPTS) {
      return `Couldn't reconnect after ${MAX_ATTEMPTS} attempts${
        detail ? `: ${detail}` : ""
      }.`;
    }
    if (detail) return detail;
    return undefined;
  })();

  return (
    <EmptyState
      variant={offline ? "offline" : "error"}
      title={title}
      description={description}
      action={
        <button
          type="button"
          onClick={() => void doRetry()}
          disabled={retrying}
          aria-busy={retrying}
          style={retryBtnStyle(retrying)}
        >
          {retrying ? "Retrying…" : "Try again"}
        </button>
      }
    />
  );
}

function retryBtnStyle(disabled: boolean): React.CSSProperties {
  return {
    padding: "6px 14px",
    fontSize: 12,
    fontFamily: "var(--font-mono)",
    background: disabled ? "var(--bg-surface)" : "var(--accent)",
    color: disabled ? "var(--text-3)" : "var(--bg-base, #fff)",
    border: "1px solid var(--border)",
    borderRadius: 6,
    cursor: disabled ? "not-allowed" : "pointer",
    opacity: disabled ? 0.55 : 1,
  };
}
