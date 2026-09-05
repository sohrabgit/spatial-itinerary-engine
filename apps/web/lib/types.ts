export type Stop = {
  poi_id: number;
  name: string;
  category: string;
  lat: number;
  lon: number;
  arrive_hhmm: string;
  depart_hhmm: string;
  dwell_minutes: number;
  walk_minutes_from_prev: number;
  walk_metres_from_prev: number;
  quietness: number | null;
  hours_known: boolean;
  why: string[];
};

export type Day = {
  day_index: number;
  stops: Stop[];
  total_walk_metres: number;
  total_walk_minutes: number;
  polyline: string;
};

export type Itinerary = {
  intent: Record<string, unknown> & { days: number; themes: string[]; ambience: string[] };
  days: Day[];
  candidates_considered: number;
  query_class: string;
  solver_status: string;
  objective: number;
  warnings: string[];
  timings_ms: Record<string, number>;
};

/**
 * Day colours are Paris Métro line colours (M2 blue, M4 magenta, M12 green),
 * brightened to hold their identity against a dark basemap. A wayfinding
 * palette designed for exactly this job -- telling routes apart on a city map
 * -- rather than three arbitrary hues.
 */
export const DAY_COLOURS = ["#3B9BE8", "#E86BB6", "#35B98A", "#F2A24E"] as const;

export function dayColour(i: number): string {
  return DAY_COLOURS[i % DAY_COLOURS.length];
}
