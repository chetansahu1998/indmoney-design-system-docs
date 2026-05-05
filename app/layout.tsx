import type { Metadata } from "next";
import "./globals.css";
// Phase 9 U2b — View Transitions keyframes for the /atlas → /projects
// flow-leaf morph. Imported globally because the pseudo-elements
// (::view-transition-old/new) attach to the document root and must be in
// scope on both the source (/atlas) and destination (/projects/[slug])
// pages for the cross-route morph to animate. Pure CSS — no JS cost.
import "./atlas/view-transitions.css";
import ToastHost from "@/components/ui/Toast";
import RootClient from "@/components/RootClient";
import AuthGate from "@/components/AuthGate";

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
    // suppressHydrationWarning is required because the inline <head>
    // script removes data-pre-hydrate before React hydrates — by design.
    // Without this, React flags every page load as a hydration mismatch.
    <html lang="en" data-pre-hydrate="true" suppressHydrationWarning>
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
        {/* Theme bootstrap — runs before first paint so /inbox,
            /onboarding, /settings, /login (which don't mount
            DocsShell/FilesShell/ProjectShell) still get the right
            data-theme. Reads the same localStorage key the shells
            write via setTheme. Default: dark. */}
        <script
          dangerouslySetInnerHTML={{
            __html:
              "(function(){try{var t=localStorage.getItem('indmoney-ds-theme');if(t!=='light'&&t!=='dark')t='dark';document.documentElement.setAttribute('data-theme',t);}catch(e){document.documentElement.setAttribute('data-theme','dark');}})()",
          }}
        />
      </head>
      <body>
        <RootClient />
        <AuthGate>{children}</AuthGate>
        <ToastHost />
      </body>
    </html>
  );
}
