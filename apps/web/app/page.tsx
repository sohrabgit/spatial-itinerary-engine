"use client";

import dynamic from "next/dynamic";
import { useState } from "react";

import Timetable from "@/components/Timetable";
import { buildItinerary } from "@/lib/api";
import type { Itinerary } from "@/lib/types";

// maplibre-gl touches `window` at module scope, so it must be both a client
// component and dynamically imported with ssr:false. Getting this wrong is an
// SSR crash that reads as a MapLibre bug rather than a Next.js one.
const ItineraryMap = dynamic(() => import("@/components/ItineraryMap"), {
  ssr: false,
  loading: () => <div className="h-full w-full bg-paper" />,
});

const EXAMPLES = [
  "3-day historic walk in Paris with quiet cafés and minimal transit",
  "One packed day — the Louvre, Notre-Dame, and somewhere good for lunch",
  "Two relaxed days, parks and galleries, nothing touristy",
];

export default function Page() {
  const [prompt, setPrompt] = useState(EXAMPLES[0]);
  const [itinerary, setItinerary] = useState<Itinerary | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [activeDay, setActiveDay] = useState<number | null>(null);
  const [hoveredStop, setHoveredStop] = useState<number | null>(null);

  async function plan(text: string) {
    setLoading(true);
    setError(null);
    try {
      setItinerary(await buildItinerary(text));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Something went wrong.");
    } finally {
      setLoading(false);
    }
  }

  return (
    <main className="flex h-dvh flex-col">
      <header className="flex flex-wrap items-center gap-x-5 gap-y-3 border-b border-rule px-5 py-3">
        <div className="flex items-baseline gap-2.5">
          <h1 className="font-display text-[22px] leading-none font-light tracking-tight">
            Flâneur
          </h1>
          <p className="text-[11.5px] text-ink-faint">Walking itineraries for Paris</p>
        </div>

        <form
          className="flex min-w-[320px] flex-1 items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (prompt.trim()) plan(prompt);
          }}
        >
          <input
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            placeholder="Describe the trip you want"
            aria-label="Describe the trip you want"
            className="min-w-0 flex-1 rounded-[3px] border border-rule bg-paper-raised px-3 py-2 text-[13px] placeholder:text-ink-faint focus:border-ink focus:outline-none"
          />
          <button
            type="submit"
            disabled={loading || !prompt.trim()}
            className="shrink-0 rounded-[3px] bg-ink px-4 py-2 text-[13px] text-paper-raised disabled:opacity-40"
          >
            {loading ? "Planning" : "Plan the walk"}
          </button>
        </form>
      </header>

      {itinerary && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-rule px-5 py-1.5 text-[11px] text-ink-soft">
          <span className="tabular-nums">
            {itinerary.candidates_considered.toLocaleString()} places considered
          </span>
          <span className="tabular-nums">
            solved in {(itinerary.timings_ms.total_ms / 1000).toFixed(1)}s
          </span>
          {itinerary.warnings.slice(0, 2).map((w) => (
            <span key={w} className="text-ink-faint">
              {w}
            </span>
          ))}
        </div>
      )}

      <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
        <div className="relative h-[45vh] min-h-0 lg:h-auto lg:flex-1">
          <ItineraryMap
            days={itinerary?.days ?? []}
            activeDay={activeDay}
            hoveredStop={hoveredStop}
            onHoverStop={setHoveredStop}
          />
        </div>

        <aside className="min-h-0 w-full overflow-y-auto border-t border-rule px-5 py-4 lg:w-[46%] lg:max-w-[720px] lg:border-t-0 lg:border-l">
          {error && (
            <div className="rounded-[3px] border border-rule bg-paper-raised p-3 text-[13px]">
              <p className="mb-1 font-medium">{error}</p>
              <p className="text-ink-soft">
                Check the planner is running on port 8000, then try again.
              </p>
            </div>
          )}

          {!itinerary && !error && (
            <div className="max-w-[52ch]">
              <p className="font-display text-[19px] leading-snug font-light">
                Describe the days you want. Flâneur reads 28,000 places across Paris,
                works out which are quiet, and routes a walk that respects opening hours.
              </p>
              <p className="mt-4 mb-2 text-[12px] text-ink-soft">Try one of these</p>
              <ul className="space-y-1.5">
                {EXAMPLES.map((ex) => (
                  <li key={ex}>
                    <button
                      onClick={() => {
                        setPrompt(ex);
                        plan(ex);
                      }}
                      className="text-left text-[13px] leading-snug text-ink-soft underline decoration-rule underline-offset-4 hover:text-ink hover:decoration-ink"
                    >
                      {ex}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {itinerary && (
            <Timetable
              days={itinerary.days}
              activeDay={activeDay}
              setActiveDay={setActiveDay}
              hoveredStop={hoveredStop}
              onHoverStop={setHoveredStop}
            />
          )}
        </aside>
      </div>
    </main>
  );
}
