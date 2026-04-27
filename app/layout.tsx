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
    <html lang="en" data-pre-hydrate="true">
      <head>
        {/* Runs before any styled paint: removes the pre-hydrate sentinel
            as soon as the parser reaches this script. Pairs with the
            globals.css rule that pins data-anim-fade items to opacity 0
            until the sentinel clears, so the user never sees raw markup
            mid-hydrate. The reduced-motion escape hatch in CSS still
            wins — this is a flash-prevention mechanism, not an
            accessibility override. */}
        <script
          dangerouslySetInnerHTML={{
            __html: "document.documentElement.removeAttribute('data-pre-hydrate')",
          }}
        />
      </head>
      <body>{children}</body>
    </html>
  );
}
