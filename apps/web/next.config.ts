import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          // strict-origin-when-cross-origin, NOT no-referrer or same-origin.
          // OSM's tile usage policy requires a valid Referer and explicitly
          // forbids restrictive referrer policies. The usual Next.js hardening
          // snippet sets no-referrer, which makes this app look like anonymous
          // scraping traffic and gets the tiles blocked -- after working fine
          // for weeks. See docs/adr/0009-map-tiles.md
          { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "X-Frame-Options", value: "DENY" },
        ],
      },
    ];
  },
};

export default nextConfig;
