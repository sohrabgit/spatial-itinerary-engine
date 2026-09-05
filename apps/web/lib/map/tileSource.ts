/**
 * The tile-source seam.
 *
 * This is the ONLY module that knows which basemap backend is in use.
 * `buildMapStyle` returns a finished StyleSpecification, so <ItineraryMap>
 * never learns whether the tiles are raster or vector. Swapping to self-hosted
 * Protomaps PMTiles later means adding a case here plus one addProtocol call --
 * a config change, not a code change.
 *
 * See docs/adr/0009-map-tiles.md
 */
import type { StyleSpecification } from "maplibre-gl";

export type TileSource =
  | {
      kind: "raster";
      tiles: string[];
      tileSize: number;
      maxzoom: number;
      attribution: string;
    }
  | { kind: "pmtiles"; url: string; theme: "light" | "dark" };

export function tileSourceFromEnv(): TileSource {
  const kind = process.env.NEXT_PUBLIC_TILE_KIND ?? "raster";
  if (kind === "pmtiles") {
    return {
      kind: "pmtiles",
      url: process.env.NEXT_PUBLIC_TILE_URL ?? "",
      theme: "light",
    };
  }
  return {
    kind: "raster",
    tiles: [
      process.env.NEXT_PUBLIC_TILE_URL ??
        "https://tile.openstreetmap.org/{z}/{x}/{y}.png",
    ],
    tileSize: 256,
    maxzoom: Number(process.env.NEXT_PUBLIC_TILE_MAXZOOM ?? 19),
    attribution:
      process.env.NEXT_PUBLIC_TILE_ATTRIBUTION ?? "© OpenStreetMap contributors",
  };
}

/** Deep ground the basemap is darkened onto. */
const GROUND = "#0C1614";

export function buildMapStyle(src: TileSource): StyleSpecification {
  if (src.kind === "pmtiles") {
    throw new Error("pmtiles backend not wired yet; see ADR 0009");
  }
  return {
    version: 8,
    // Raster OSM tiles cannot be restyled -- there is no dark variant to switch
    // to. Instead the tiles are desaturated and pushed down in brightness over
    // a dark ground, which yields a muted basemap the Metro route colours can
    // sit on. Not a true inversion, but honest: no third-party dark tiles.
    sources: {
      osm: {
        type: "raster",
        tiles: src.tiles,
        tileSize: src.tileSize,
        maxzoom: src.maxzoom,
        attribution: src.attribution,
      },
    },
    layers: [
      { id: "ground", type: "background", paint: { "background-color": GROUND } },
      {
        id: "osm",
        type: "raster",
        source: "osm",
        paint: {
          "raster-opacity": 0.42,
          "raster-saturation": -0.88,
          "raster-brightness-min": 0.02,
          "raster-brightness-max": 0.42,
          "raster-contrast": -0.12,
        },
      },
    ],
  };
}
