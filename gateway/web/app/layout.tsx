import type { Metadata, Viewport } from "next"

import "./globals.css"
import { Providers } from "@/components/providers"
import { zhCN } from "@/lib/i18n/zh-CN"

export const metadata: Metadata = {
  title: { default: zhCN.app.name, template: `%s · ${zhCN.app.name}` },
  robots: { index: false, follow: false },
}

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#f4f5f7" },
    { media: "(prefers-color-scheme: dark)", color: "#161617" },
  ],
}

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <body>
        <Providers>{children}</Providers>
      </body>
    </html>
  )
}
