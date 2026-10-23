import type { Metadata, Viewport } from "next"

import "./globals.css"
import { ThemeProvider } from "@/components/theme-provider"
import { ServiceWorker } from "@/components/service-worker"
import { Toaster } from "@/components/ui/sonner"

export const metadata: Metadata = {
  title: "CZL 笔记",
  description: "在手机上只读查看 CZL Mail 的笔记",
  manifest: "/manifest.json",
  appleWebApp: { capable: true, title: "CZL 笔记", statusBarStyle: "default" },
  icons: { apple: "/images/apple-touch-icon.png" },
  // Notes are private; keep the login page out of search engines.
  robots: { index: false, follow: false },
}

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  viewportFit: "cover",
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#F6F7F8" },
    { media: "(prefers-color-scheme: dark)", color: "#10141C" },
  ],
}

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="zh-CN" suppressHydrationWarning className="antialiased">
      <body>
        <ThemeProvider>{children}</ThemeProvider>
        <Toaster position="top-center" />
        <ServiceWorker />
      </body>
    </html>
  )
}
