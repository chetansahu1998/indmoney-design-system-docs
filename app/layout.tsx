import type { Metadata } from "next";
import "./globals.css";

const BRAND = process.env.NEXT_PUBLIC_BRAND ?? "indmoney";
const BRAND_LABELS: Record<string, string> = {
  indmoney: "INDmoney",
  tickertape: "Tickertape",
};

const brandLabel = BRAND_LABELS[BRAND] ?? "INDmoney";

export const metadata: Metadata = {
  title: `${brandLabel} DS · Foundations`,
  description: `${brandLabel} Design System — Typography, Color, Spacing, Motion, Iconography foundations`,
  applicationName: `${brandLabel} Design System`,
  authors: [{ name: brandLabel }],
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
