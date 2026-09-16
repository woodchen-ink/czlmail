import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Wails 把前端当静态资源嵌进二进制, 运行时没有 Node, 必须走静态导出。
  output: "export",
  trailingSlash: true,

  // 静态导出下没有服务端, next/image 的优化管线不可用。
  images: { unoptimized: true },

  // wails dev 经自己的代理转发 next dev, 代理不处理 gzip, 会导致浏览器解码失败。
  compress: false,
  // 让 wails dev(34115) 能加载 next dev 的 HMR 与开发资源。
  allowedDevOrigins: ["localhost", "127.0.0.1"],
};

export default nextConfig;
