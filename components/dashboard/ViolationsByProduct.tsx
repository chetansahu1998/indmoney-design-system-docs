"use client";

/**
 * Bar chart of active violations by product. Recharts component is the
 * primary content; lazy-loaded by DashboardShell into chunks/dashboard.
 */

import {
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { ProductCount } from "@/lib/dashboard/client";

interface Props {
  data: ProductCount[];
}

export default function ViolationsByProduct({ data }: Props) {
  return (
    <Panel title="Active violations by product">
      {data.length === 0 ? (
        <Empty>No active violations.</Empty>
      ) : (
        <ResponsiveContainer width="100%" height={240}>
          <BarChart data={data} margin={{ top: 10, right: 10, bottom: 32, left: 4 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
            <XAxis
              dataKey="product"
              tick={{ fontSize: 11, fontFamily: "var(--font-mono)", fill: "var(--text-3)" }}
              interval={0}
              angle={-30}
              textAnchor="end"
            />
            <YAxis
              tick={{ fontSize: 11, fontFamily: "var(--font-mono)", fill: "var(--text-3)" }}
              allowDecimals={false}
            />
            <Tooltip
              cursor={{ fill: "rgba(124,58,237,0.08)" }}
              contentStyle={{
                fontSize: 11,
                fontFamily: "var(--font-mono)",
                background: "var(--bg-surface)",
                border: "1px solid var(--border)",
              }}
            />
            <Bar dataKey="active" fill="var(--accent)" radius={[4, 4, 0, 0]} />
          </BarChart>
        </ResponsiveContainer>
      )}
    </Panel>
  );
}

function Panel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section
      style={{
        padding: 16,
        border: "1px solid var(--border)",
        borderRadius: 10,
        background: "var(--bg-surface)",
      }}
    >
      <h3
        style={{
          fontSize: 12,
          fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
          textTransform: "uppercase",
          letterSpacing: 0.6,
          margin: 0,
          marginBottom: 12,
        }}
      >
        {title}
      </h3>
      {children}
    </section>
  );
}

function Empty({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        height: 200,
        display: "grid",
        placeItems: "center",
        fontSize: 12,
        fontFamily: "var(--font-mono)",
        color: "var(--text-3)",
      }}
    >
      {children}
    </div>
  );
}
