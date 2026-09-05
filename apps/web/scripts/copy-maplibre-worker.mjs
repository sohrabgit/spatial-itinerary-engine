/**
 * maplibre-gl v6 is ESM-only and loads its worker as a SEPARATE module:
 *   new Worker(new URL("./maplibre-gl-worker.mjs", import.meta.url), {type:"module"})
 *
 * Next's bundler does not emit that URL, so the worker never starts. The
 * failure is silent and specific: raster tiles render fine (decoded on the main
 * thread) while every GeoJSON source stays `isSourceLoaded() === false` forever
 * with no console error -- so routes and markers simply never appear.
 *
 * Fix: serve the worker (and the shared chunk it imports) ourselves, then point
 * MapLibre at it with setWorkerUrl(). Run on prebuild/predev so the copies can
 * never drift from the installed version.
 */
import { copyFile, mkdir } from "node:fs/promises";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";

const require = createRequire(import.meta.url);
const dist = dirname(require.resolve("maplibre-gl/dist/maplibre-gl.mjs"));
const out = join(process.cwd(), "public", "maplibre");

await mkdir(out, { recursive: true });
for (const f of ["maplibre-gl-worker.mjs", "maplibre-gl-shared.mjs"]) {
  await copyFile(join(dist, f), join(out, f));
}
console.log("maplibre worker + shared chunk copied to public/maplibre/");
