"use client";

/**
 * Stacked area chart of active vs fixed violations over the configured
 * weekly window. Recharts component lazy-loaded by DashboardShell.
 */

import {
  Area,
  AreaChart,
  CartesianGrid,
  Legend,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { TrendBucket } from "@/lib/dashboard/client";

interface Props {
  data: TrendBucket[];
}

export default function SeverityTrend({ data }: Props) {
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
        Trend (active vs fixed)
      </h3>
      {data.length === 0 ? (
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
          No data in the selected window.
        </div>
      ) : (
        <ResponsiveContainer width="100%" height={240}>
          <AreaChart data={data} margin={{ top: 10, right: 10, bottom: 4, left: 4 }}>
            <defs>
              <linearGradient id="g-active" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="#dc2626" stopOpacity={0.6} />
                <stop offset="100%" stopColor="#dc2626" stopOpacity={0} />
              </linearGradient>
              <linearGradient id="g-fixed" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="#16a34a" stopOpacity={0.6} />
                <stop offset="100%" stopColor="#16a34a" stopOpacity={0} />
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
            <XAxis
              dataKey="week_start"
              tick={{ fontSize: 11, fontFamily: "var(--font-mono)", fill: "var(--text-3)" }}
            />
            <YAxis
              tick={{ fontSize: 11, fontFamily: "var(--font-mono)", fill: "var(--text-3)" }}
              allowDecimals={false}
            />
            <Tooltip
              contentStyle={{
                fontSize: 11,
                fontFamily: "var(--font-mono)",
                background: "var(--bg-surface)",
                border: "1px solid var(--border)",
              }}
            />
            <Legend wrapperStyle={{ fontSize: 11, fontFamily: "var(--font-mono)" }} />
            <Area
              type="monotone"
              dataKey="active"
              stroke="#dc2626"
              strokeWidth={2}
              fill="url(#g-active)"
              name="Active"
            />
            <Area
              type="monotone"
              dataKey="fixed"
              stroke="#16a34a"
              strokeWidth={2}
              fill="url(#g-fixed)"
              name="Fixed"
            />
          </AreaChart>
        </ResponsiveContainer>
      )}
    </section>
  );
}
