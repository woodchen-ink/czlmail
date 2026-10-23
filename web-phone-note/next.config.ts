import path from "node:path"
import type { NextConfig } from "next"

const nextConfig: NextConfig = {
  // 只托管静态文件: 页面在浏览器里直接请求 JMAP 服务器, 没有自己的后端。
  output: "export",
  trailingSlash: true,
  images: { unoptimized: true },
  // Markdown 解析与桌面端共用 desktop/frontend/lib/notes, 根目录放到仓库根才解析得到。
  turbopack: { root: path.join(import.meta.dirname, "..") },
}

export default nextConfig
