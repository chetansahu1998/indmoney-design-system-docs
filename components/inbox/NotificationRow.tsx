"use client";

/**
 * NotificationRow — Phase 5 U7. One row per notification under the
 * Mentions tab in /inbox. Click navigates to the underlying surface
 * (DRD block / decision / violation); marks the notification as read.
 */

import Link from "next/link";
import type { NotificationRecord } from "@/lib/notifications/client";

interface Props {
  notification: NotificationRecord;
  onClick: (id: string) => void;
}

const KIND_LABELS: Record<NotificationRecord["kind"], string> = {
  mention: "@mention",
  decision_made: "Decision",
  decision_superseded: "Superseded",
  comment_resolved: "Resolved",
  drd_edited_on_owned_flow: "DRD edit",
};

function relativeTime(iso: string): string {
  const t = new Date(iso).getTime();
  const diff = Date.now() - t;
  if (Number.isNaN(diff) || diff < 0) return "";
  const sec = Math.round(diff / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.round(min / 60);
  if (hr < 24) return `${hr}h ago`;
  return new Date(iso).toLocaleDateString();
}

function snippet(payload: string | undefined): string {
  if (!payload) return "";
  try {
    const obj = JSON.parse(payload) as { body_snippet?: string };
    return obj.body_snippet ?? "";
  } catch {
    return "";
  }
}

function targetHref(n: NotificationRecord): string {
  // For mentions on a comment we navigate to the project's flow + the
  // comment target. Phase 6 polish adds a deep-link query (?comment=…)
  // that scrolls + expands the thread. For now we link to the project.
  // Without a slug we fall back to /inbox.
  return "/inbox";
}

export default function NotificationRow({ notification, onClick }: Props) {
  const isUnread = !notification.read_at;
  const label = KIND_LABELS[notification.kind] ?? notification.kind;
  const preview = snippet(notification.payload_json);
  const href = targetHref(notification);

  return (
    <li
      data-testid="notification-row"
      data-notification-id={notification.id}
      style={{
        listStyle: "none",
        display: "grid",
        gridTemplateColumns: "auto 1fr auto",
        alignItems: "center",
        gap: 12,
        padding: "10px 14px",
        border: "1px solid var(--border)",
        borderLeft: `3px solid ${isUnread ? "var(--accent)" : "var(--border)"}`,
        borderRadius: 8,
        background: isUnread ? "var(--bg-surface)" : "transparent",
      }}
    >
      <span
        style={{
          fontSize: 10,
          fontFamily: "var(--font-mono)",
          textTransform: "uppercase",
          letterSpacing: 0.6,
          color: isUnread ? "var(--accent)" : "var(--text-3)",
          padding: "2px 8px",
          border: `1px solid ${isUnread ? "var(--accent)" : "var(--border)"}`,
          borderRadius: 999,
        }}
      >
        {label}
      </span>
      <Link
        href={href}
        onClick={() => onClick(notification.id)}
        style={{
          minWidth: 0,
          color: "var(--text-1)",
          textDecoration: "none",
          fontSize: 13,
        }}
      >
        <div style={{ marginBottom: 2 }}>
          {notification.actor_user_id
            ? `${notification.actor_user_id.slice(0, 8)} mentioned you`
            : "You have a new notification"}
        </div>
        {preview && (
          <div
            style={{
              fontSize: 11,
              fontFamily: "var(--font-mono)",
              color: "var(--text-3)",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            “{preview}”
          </div>
        )}
      </Link>
      <span
        style={{
          fontSize: 11,
          fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
        }}
      >
        {relativeTime(notification.created_at)}
      </span>
    </li>
  );
}
