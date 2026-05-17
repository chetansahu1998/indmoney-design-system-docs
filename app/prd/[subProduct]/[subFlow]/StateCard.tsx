"use client";

/**
 * StateCard — renders a single prd_state with all typed children as
 * structured tables (NOT prose blobs).
 *
 * The point of the typed-stems schema (U4) was to keep events, copy,
 * a11y notes, and acceptance criteria as first-class rows so reviewers
 * can scan them at a glance instead of hunting through paragraphs. The
 * card mirrors that intent: each stem has its own labelled block, every
 * row is rendered as a table cell.
 *
 * Markdown columns (condition_md, design_handling_md, fe_handling_md)
 * are rendered as plain pre-wrap text for v1 — adding a real Markdown
 * renderer here was deemed out-of-scope. PMs writing rich content can
 * still see line breaks + emphasis as raw markdown, which is the same
 * experience the ind-prd skill gives them.
 */

import type { PRDStateFull } from "./types";

interface Props {
  state: PRDStateFull;
}

export function StateCard({ state }: Props) {
  if (state.deleted_at) {
    return null; // soft-deleted; never surface in the document view
  }
  const hasAny =
    (state.acceptance_criteria?.length ?? 0) > 0 ||
    (state.events?.length ?? 0) > 0 ||
    (state.copy_strings?.length ?? 0) > 0 ||
    (state.edge_cases?.length ?? 0) > 0 ||
    (state.a11y_notes?.length ?? 0) > 0 ||
    (state.frame_tags?.length ?? 0) > 0 ||
    state.condition_md ||
    state.design_handling_md ||
    state.fe_handling_md;

  return (
    <section className="state-card">
      <header className="state-card__head">
        <h4>{state.label}</h4>
        {state.frame_name && (
          <span className="state-card__frame" title="Frame name">
            {state.frame_name}
          </span>
        )}
      </header>

      {state.condition_md && (
        <Block label="Condition">
          <pre className="md">{state.condition_md}</pre>
        </Block>
      )}

      {(state.acceptance_criteria?.length ?? 0) > 0 && (
        <Block label="Acceptance criteria">
          <ul>
            {state.acceptance_criteria!.map((c) => (
              <li key={c.id}>{c.criterion}</li>
            ))}
          </ul>
        </Block>
      )}

      {(state.events?.length ?? 0) > 0 && (
        <Block label="Mixpanel events">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Fires on</th>
                <th>Properties</th>
              </tr>
            </thead>
            <tbody>
              {state.events!.map((e) => (
                <tr key={e.id}>
                  <td>
                    <code>{e.name}</code>
                  </td>
                  <td>{e.fires_on}</td>
                  <td>
                    <pre className="schema">{prettyJSON(e.properties_schema)}</pre>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Block>
      )}

      {(state.copy_strings?.length ?? 0) > 0 && (
        <Block label="Copy">
          <table>
            <thead>
              <tr>
                <th>Key</th>
                <th>Value</th>
                <th>Locale</th>
              </tr>
            </thead>
            <tbody>
              {state.copy_strings!.map((c) => (
                <tr key={c.id}>
                  <td>
                    <code>{c.key}</code>
                  </td>
                  <td>{c.value}</td>
                  <td>{c.locale || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Block>
      )}

      {(state.edge_cases?.length ?? 0) > 0 && (
        <Block label="Edge cases">
          <ul>
            {state.edge_cases!.map((e) => (
              <li key={e.id}>{e.edge_case}</li>
            ))}
          </ul>
        </Block>
      )}

      {(state.a11y_notes?.length ?? 0) > 0 && (
        <Block label="Accessibility notes">
          <ul>
            {state.a11y_notes!.map((n) => (
              <li key={n.id}>{n.note}</li>
            ))}
          </ul>
        </Block>
      )}

      {state.design_handling_md && (
        <Block label="Design handling">
          <pre className="md">{state.design_handling_md}</pre>
        </Block>
      )}

      {state.fe_handling_md && (
        <Block label="Frontend handling">
          <pre className="md">{state.fe_handling_md}</pre>
        </Block>
      )}

      {(state.frame_tags?.length ?? 0) > 0 && (
        <Block label="Bound frames">
          <ul className="frames">
            {state.frame_tags!.map((t) => (
              <li key={t.id}>
                <code>{t.figma_node_id}</code>
                {t.variant ? ` (${t.variant})` : ""}
              </li>
            ))}
          </ul>
        </Block>
      )}

      {!hasAny && (
        <div className="state-card__thin">
          No typed stems authored yet. Use{" "}
          <code>/ind-prd add-state {state.label}</code> in Claude to seed
          criteria, events, and copy.
        </div>
      )}

      <style jsx>{`
        .state-card {
          border: 1px solid var(--border, rgba(255, 255, 255, 0.08));
          border-radius: 8px;
          padding: 16px;
          background: var(--surface-1, rgba(255, 255, 255, 0.02));
          display: flex;
          flex-direction: column;
          gap: 12px;
        }
        .state-card__head {
          display: flex;
          justify-content: space-between;
          align-items: baseline;
          gap: 12px;
        }
        .state-card__head h4 {
          margin: 0;
          font-size: 14px;
          font-weight: 600;
          color: var(--text-1);
        }
        .state-card__frame {
          font-size: 11px;
          color: var(--text-3);
          font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
        }
        .state-card__thin {
          font-size: 12px;
          color: var(--text-3);
          padding: 8px 0;
          line-height: 1.55;
        }
        code {
          font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
          font-size: 11px;
          background: var(--surface-1, rgba(255, 255, 255, 0.04));
          padding: 1px 5px;
          border-radius: 4px;
          color: var(--text-2);
        }
        pre.md {
          margin: 0;
          font-family: inherit;
          font-size: 12px;
          color: var(--text-2);
          white-space: pre-wrap;
          line-height: 1.55;
        }
        pre.schema {
          margin: 0;
          font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
          font-size: 10px;
          color: var(--text-3);
          white-space: pre-wrap;
          max-width: 280px;
        }
        ul {
          margin: 0;
          padding-left: 18px;
          font-size: 12px;
          color: var(--text-2);
          line-height: 1.55;
        }
        ul.frames {
          list-style: none;
          padding: 0;
        }
        table {
          width: 100%;
          border-collapse: collapse;
          font-size: 12px;
        }
        th,
        td {
          text-align: left;
          padding: 6px 8px;
          border-bottom: 1px solid var(--border, rgba(255, 255, 255, 0.06));
          vertical-align: top;
          color: var(--text-2);
        }
        th {
          color: var(--text-3);
          font-weight: 500;
          font-size: 10px;
          text-transform: uppercase;
          letter-spacing: 0.04em;
        }
      `}</style>
    </section>
  );
}

function Block({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="block">
      <h5>{label}</h5>
      <div>{children}</div>
      <style jsx>{`
        .block {
          display: flex;
          flex-direction: column;
          gap: 4px;
        }
        h5 {
          margin: 0;
          font-size: 10px;
          letter-spacing: 0.06em;
          text-transform: uppercase;
          color: var(--text-3);
          font-weight: 500;
        }
      `}</style>
    </div>
  );
}

// prettyJSON tries to pretty-print a properties_schema string. The column
// is stored verbatim (could be JSON, could be free-form), so we fall back
// to the raw string when parse fails.
function prettyJSON(s: string): string {
  if (!s) return "";
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}
