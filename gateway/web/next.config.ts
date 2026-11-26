import type { NextConfig } from "next"

// The gateway (Go) serves the static export under /admin/ and the API under /api/.
// `next dev` proxies /api to a gateway on GATEWAY_DEV_URL (default http://localhost:8080).
const isDev = process.env.NODE_ENV === "development"
const gatewayDev = process.env.GATEWAY_DEV_URL ?? "http://localhost:8080"

const nextConfig: NextConfig = {
  basePath: "/admin",
  trailingSlash: true,
  images: { unoptimized: true },
  ...(isDev
    ? {
        async rewrites() {
          return [{ source: "/api/:path*", destination: `${gatewayDev}/api/:path*`, basePath: false }]
        },
      }
    : { output: "export" }),
}

export default nextConfig
