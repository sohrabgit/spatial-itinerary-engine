"use client";

import {
  type GeoJSONSource,
  LngLatBounds,
  Map as MLMap,
  type MapLayerMouseEvent,
  NavigationControl,
  setWorkerUrl,
} from "maplibre-gl";
import { useEffect, useRef } from "react";

import { buildMapStyle, tileSourceFromEnv } from "@/lib/map/tileSource";
import { decodePolyline6 } from "@/lib/polyline";
import { type Day, dayColour } from "@/lib/types";

import "maplibre-gl/dist/maplibre-gl.css";

type Props = {
  days: Day[];
  activeDay: number | null;
  hoveredStop: number | null;
  onHoverStop: (poiId: number | null) => void;
};

const PARIS: [number, number] = [2.3438, 48.8566];

// maplibre-gl v6 loads its worker as a separate ES module resolved from
// import.meta.url. Next's bundler does not emit that URL, so the worker never
// starts -- and the failure is silent and misleading: raster tiles render
// (main-thread decode) while every GeoJSON source stays unloaded forever, so
// routes and stops never appear and nothing is logged.
//
// scripts/copy-maplibre-worker.mjs copies the worker and its shared chunk into
// public/maplibre on predev/prebuild, so this URL is always in sync.
setWorkerUrl("/maplibre/maplibre-gl-worker.mjs");

export default function ItineraryMap({ days, activeDay, hoveredStop, onHoverStop }: Props) {
  const container = useRef<HTMLDivElement>(null);
  const map = useRef<MLMap | null>(null);
  const ready = useRef(false);
  // Latest-callback ref: the map's event handlers are registered once on load,
  // but must always call the CURRENT onHoverStop. Assigning in an effect rather
  // than during render -- mutating a ref while rendering is unsafe under
  // concurrent rendering, and React 19 flags it.
  const onHover = useRef(onHoverStop);
  useEffect(() => {
    onHover.current = onHoverStop;
  }, [onHoverStop]);

  useEffect(() => {
    if (!container.current || map.current) return;
    const m = new MLMap({
      container: container.current,
      style: buildMapStyle(tileSourceFromEnv()),
      center: PARIS,
      zoom: 12.4,
      attributionControl: { compact: true },
    });
    m.addControl(new NavigationControl({ showCompass: false }), "bottom-right");
    m.on("load", () => {
      ready.current = true;
      // One GeoJSON source per concern, not one Marker per stop: DOM markers
      // force a reflow every frame while panning and visibly stutter at ~30
      // stops. This is the most common MapLibre performance mistake.
      m.addSource("routes", { type: "geojson", data: empty() });
      m.addSource("stops", { type: "geojson", data: empty() });

      m.addLayer({
        id: "routes-casing",
        type: "line",
        source: "routes",
        layout: { "line-cap": "round", "line-join": "round" },
        paint: {
          "line-color": "#0C1614",
          "line-width": ["interpolate", ["linear"], ["zoom"], 11, 5, 16, 9],
          "line-opacity": ["case", ["get", "dim"], 0.2, 0.75],
        },
      });
      m.addLayer({
        id: "routes",
        type: "line",
        source: "routes",
        layout: { "line-cap": "round", "line-join": "round" },
        paint: {
          "line-color": ["get", "colour"],
          "line-width": ["interpolate", ["linear"], ["zoom"], 11, 2.5, 16, 5],
          "line-opacity": ["case", ["get", "dim"], 0.18, 0.95],
        },
      });
      m.addLayer({
        id: "stops",
        type: "circle",
        source: "stops",
        paint: {
          "circle-radius": ["case", ["get", "active"], 10, 6],
          "circle-color": "#0C1614",
          "circle-stroke-color": ["get", "colour"],
          "circle-stroke-width": ["case", ["get", "active"], 4, 2.5],
          "circle-opacity": ["case", ["get", "dim"], 0.3, 1],
          "circle-stroke-opacity": ["case", ["get", "dim"], 0.25, 1],
        },
      });
      // No symbol/text layer here on purpose. MapLibre resolves text through
      // a `glyphs` URL in the style, and this style deliberately has none --
      // adding one would mean fetching fonts from a third party, which breaks
      // the zero-external-dependency mandate. Worse, the missing glyphs left
      // the style permanently unloaded, so NOTHING painted: no routes, no
      // stops, no error. Stop numbers live in the timetable, and hovering
      // links the two views.

      m.on("mousemove", "stops", (e: MapLayerMouseEvent) => {
        m.getCanvas().style.cursor = "pointer";
        const id = e.features?.[0]?.properties?.poi_id;
        if (typeof id === "number") onHover.current(id);
      });
      m.on("mouseleave", "stops", () => {
        m.getCanvas().style.cursor = "";
        onHover.current(null);
      });
    });
    map.current = m;
    if (process.env.NODE_ENV !== "production") {
      (window as unknown as { __map?: MLMap }).__map = m;
    }
    return () => {
      m.remove();
      map.current = null;
      ready.current = false;
    };
  }, []);

  useEffect(() => {
    const m = map.current;
    if (!m) return;
    const apply = () => {
  if (!m.getSource("routes")) return;
      const dim = (i: number) => activeDay !== null && activeDay !== i;

      const routes = {
        type: "FeatureCollection" as const,
        features: days
          .filter((d) => d.polyline)
          .map((d) => ({
            type: "Feature" as const,
            properties: { colour: dayColour(d.day_index), dim: dim(d.day_index) },
            geometry: {
              type: "LineString" as const,
              coordinates: decodePolyline6(d.polyline),
            },
          })),
      };
      const stops = {
        type: "FeatureCollection" as const,
        features: days.flatMap((d) =>
          d.stops.map((s, i) => ({
            type: "Feature" as const,
            properties: {
              poi_id: s.poi_id,
              n: String(i + 1),
              colour: dayColour(d.day_index),
              dim: dim(d.day_index),
              active: hoveredStop === s.poi_id,
            },
            geometry: { type: "Point" as const, coordinates: [s.lon, s.lat] },
          })),
        ),
      };
      (m.getSource("routes") as GeoJSONSource).setData(routes);
      (m.getSource("stops") as GeoJSONSource).setData(stops);

      const pts = days.flatMap((d) => d.stops.map((s) => [s.lon, s.lat] as [number, number]));
      if (pts.length > 1) {
        const b = pts.reduce(
          (acc, p) => acc.extend(p),
          new LngLatBounds(pts[0], pts[0]),
        );
        m.fitBounds(b, { padding: 72, maxZoom: 15.5, duration: 700 });
      }
    };
    if (ready.current) apply();
    else m.once("load", apply);
  }, [days, activeDay, hoveredStop]);

  return <div ref={container} className="h-full w-full" />;
}

function empty() {
  return { type: "FeatureCollection" as const, features: [] };
}
