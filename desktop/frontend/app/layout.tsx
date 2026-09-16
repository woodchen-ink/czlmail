import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";

import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { themeBootScript } from "@/lib/theme";

import "./globals.css";

// next/font 在构建期下载并自托管字体, 导出产物里是本地文件,
// 运行时不联网 —— 桌面端离线也能正确渲染。
const geistSans = Geist({ variable: "--font-geist-sans", subsets: ["latin"] });
const geistMono = Geist_Mono({ variable: "--font-geist-mono", subsets: ["latin"] });

export const metadata: Metadata = {
  title: "CZL Mail",
  description: "Stalwart 邮箱桌面客户端",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html
      lang="zh-CN"
      className={`${geistSans.variable} ${geistMono.variable} h-full antialiased`}
      // 首帧前的内联脚本会改 class，服务端导出的 HTML 与之必然不一致，属预期。
      suppressHydrationWarning
    >
      <head>
        {/* 同步执行，赶在首帧绘制前把 dark class 挂上，避免暗色用户启动时闪白。 */}
        <script dangerouslySetInnerHTML={{ __html: themeBootScript }} />
      </head>
      {/* 主壳锁死视口高度并禁止整页滚动: 三栏各自内部滚动,
          否则邮件列表变长会把整个窗口撑出滚动条, 侧栏与工具条跟着跑掉。 */}
      <body className="h-full overflow-hidden">
        <TooltipProvider delayDuration={300}>
          {children}
          <Toaster position="bottom-right" />
        </TooltipProvider>
      </body>
    </html>
  );
}
