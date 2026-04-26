"use client";
import { motion } from "framer-motion";
import {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
} from "@/components/ui/table";

export default function DSTable({
  headers,
  rows,
}: {
  headers: string[];
  rows: React.ReactNode[][];
}) {
  return (
    <div style={{ border: "1px solid var(--border)", borderRadius: 8, overflow: "hidden" }}>
      <Table>
        <TableHeader>
          <TableRow style={{ background: "var(--bg-surface)", borderBottom: "1px solid var(--border)" }}>
            {headers.map((h) => (
              <TableHead
                key={h}
                style={{
                  fontSize: 11, fontWeight: 600,
                  color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.07em",
                  padding: "12px 16px",
                  whiteSpace: "nowrap",
                  background: "var(--bg-surface)",
                }}
              >
                {h}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row, ri) => (
            <motion.tr
              key={ri}
              initial={{ opacity: 0 }}
              whileInView={{ opacity: 1 }}
              viewport={{ once: true, margin: "-20px" }}
              transition={{ delay: ri * 0.03, duration: 0.3 }}
              whileHover={{ backgroundColor: "var(--bg-surface)" }}
              style={{
                borderBottom: ri < rows.length - 1 ? "1px solid var(--border)" : "none",
                transition: "background 0.15s",
              }}
            >
              {row.map((cell, ci) => (
                <TableCell
                  key={ci}
                  style={{
                    fontSize: 13, color: "var(--text-2)",
                    padding: "14px 16px",
                    verticalAlign: "middle",
                    borderBottom: "none",
                  }}
                >
                  {cell}
                </TableCell>
              ))}
            </motion.tr>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
