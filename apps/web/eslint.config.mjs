// Next 16 removed the `next lint` command; eslint-config-next now ships a
// native flat config, so it is imported directly rather than wrapped in
// FlatCompat (which fails on a circular reference in this version).
import coreWebVitals from "eslint-config-next/core-web-vitals";
import typescript from "eslint-config-next/typescript";

const config = [
  ...(Array.isArray(coreWebVitals) ? coreWebVitals : [coreWebVitals]),
  ...(Array.isArray(typescript) ? typescript : [typescript]),
  {
    ignores: [".next/**", "public/maplibre/**", "next-env.d.ts", "node_modules/**"],
  },
];

export default config;
