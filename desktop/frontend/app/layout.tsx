import type { Metadata, Viewport } from "next";

import { LocaleProvider } from "@/components/locale-provider";
import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { nativeBootScript } from "@/lib/native-bridge";
import { themeBootScript } from "@/lib/theme";

import "./globals.css";

// 移动端壳: 铺满刘海与 Home 指示条区域 (安全区由各处 env(safe-area-inset-*) 让出);
// 禁止缩放, 否则 iOS 聚焦 16px 以下的输入框时会自动放大整页。
export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  maximumScale: 1,
  viewportFit: "cover",
};

export const metadata: Metadata = {
  title: "CZL Mail",
  description: "Stalwart 邮箱桌面客户端",
};

// iPad 的 navigator.platform 也是 MacIntel, 要排除原生壳 (native 类由 nativeBootScript 先挂上)。
const macBootScript = `if (/Mac/.test(navigator.platform) && !document.documentElement.classList.contains("native")) document.documentElement.classList.add("mac");`;

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html
      lang="zh-CN"
      className="h-full antialiased"
      // 首帧前的内联脚本会改 class，服务端导出的 HTML 与之必然不一致，属预期。
      suppressHydrationWarning
    >
      <head>
        {/* 同步执行，赶在首帧绘制前把 dark class 挂上，避免暗色用户启动时闪白。 */}
        {/* 移动端壳里装上 window.go / window.runtime, 必须先于一切绑定调用。 */}
        <script dangerouslySetInnerHTML={{ __html: nativeBootScript }} />
        <script dangerouslySetInnerHTML={{ __html: themeBootScript }} />
        {/* macOS 窗口隐藏了标题栏, 红绿灯浮在网页左上角; 同样赶在首帧前打上标记, 好让下面的标题条占位。 */}
        <script dangerouslySetInnerHTML={{ __html: macBootScript }} />
      </head>
      {/* 主壳锁死视口高度并禁止整页滚动: 三栏各自内部滚动,
          否则邮件列表变长会把整个窗口撑出滚动条, 侧栏与工具条跟着跑掉。 */}
      <body className="h-full overflow-hidden">
        <LocaleProvider>
          <TooltipProvider delayDuration={300}>
            <div className="flex h-full flex-col pt-[env(safe-area-inset-top)]">
              {/* 仅 macOS 显示(见 globals.css 的 html.mac): 给红绿灯让出位置, 同时充当拖动窗口的把手。 */}
              <div
                className="mac-titlebar bg-sidebar border-border text-muted-foreground h-9 shrink-0 items-center justify-center border-b text-xs font-medium select-none"
                style={{ "--wails-draggable": "drag" } as React.CSSProperties}
              >
                CZL Mail
              </div>
              <div className="min-h-0 flex-1">{children}</div>
            </div>
            <Toaster position="bottom-right" />
          </TooltipProvider>
        </LocaleProvider>
      </body>
    </html>
  );
}
