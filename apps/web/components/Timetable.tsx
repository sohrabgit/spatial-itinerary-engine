"use client";

import { type Day, dayColour, type Stop } from "@/lib/types";

/**
 * The three days as parallel columns on one shared clock axis.
 *
 * Stops are positioned and sized by real time, so a 90-minute museum occupies
 * more vertical space than a 25-minute church and walking legs are literal
 * gaps. You can read the shape of a day -- and compare all three at once --
 * which a list of cards cannot show.
 */

const PX_PER_MIN = 1.35;

function toMin(hhmm: string): number {
  const [h, m] = hhmm.split(":").map(Number);
  return h * 60 + m;
}

function bounds(days: Day[]): [number, number] {
  const all = days.flatMap((d) => d.stops);
  if (!all.length) return [9 * 60, 19 * 60];
  const lo = Math.min(...all.map((s) => toMin(s.arrive_hhmm)));
  const hi = Math.max(...all.map((s) => toMin(s.depart_hhmm)));
  return [Math.floor(lo / 60) * 60, Math.ceil(hi / 60) * 60];
}

export default function Timetable({
  days,
  activeDay,
  setActiveDay,
  hoveredStop,
  onHoverStop,
}: {
  days: Day[];
  activeDay: number | null;
  setActiveDay: (d: number | null) => void;
  hoveredStop: number | null;
  onHoverStop: (id: number | null) => void;
}) {
  const [lo, hi] = bounds(days);
  const height = (hi - lo) * PX_PER_MIN;
  const hours = Array.from({ length: (hi - lo) / 60 + 1 }, (_, i) => lo + i * 60);

  return (
    <div className="flex gap-3 pb-8">
      {/* Shared clock axis */}
      <div className="relative w-11 shrink-0" style={{ height }} aria-hidden>
        {hours.map((h) => (
          <div
            key={h}
            className="absolute right-0 -translate-y-1/2 text-[11px] tabular-nums text-ink-faint"
            style={{ top: (h - lo) * PX_PER_MIN }}
          >
            {String(Math.floor(h / 60)).padStart(2, "0")}:00
          </div>
        ))}
      </div>

      <div className="flex flex-1 gap-2">
        {days.map((day) => {
          const dim = activeDay !== null && activeDay !== day.day_index;
          const colour = dayColour(day.day_index);
          return (
            <section
              key={day.day_index}
              className="flex-1 min-w-0 transition-opacity"
              style={{ opacity: dim ? 0.35 : 1 }}
              onMouseEnter={() => setActiveDay(day.day_index)}
              onMouseLeave={() => setActiveDay(null)}
            >
              <header className="mb-2 flex items-baseline justify-between gap-2 border-b pb-1.5"
                      style={{ borderColor: colour }}>
                <h2 className="font-display text-[15px] font-normal">
                  Day {day.day_index + 1}
                </h2>
                <span className="text-[11px] tabular-nums text-ink-soft">
                  {(day.total_walk_metres / 1000).toFixed(1)} km
                </span>
              </header>

              <div className="relative" style={{ height }}>
                {hours.map((h) => (
                  <div
                    key={h}
                    className="absolute inset-x-0 border-t border-rule/60"
                    style={{ top: (h - lo) * PX_PER_MIN }}
                    aria-hidden
                  />
                ))}
                {day.stops.map((s, i) => (
                  <StopBlock
                    key={s.poi_id}
                    stop={s}
                    index={i}
                    colour={colour}
                    lo={lo}
                    active={hoveredStop === s.poi_id}
                    onHover={onHoverStop}
                  />
                ))}
                {!day.stops.length && (
                  <p className="pt-4 text-[12px] text-ink-faint">
                    Nothing scheduled. Widen the request or allow more walking.
                  </p>
                )}
              </div>
            </section>
          );
        })}
      </div>
    </div>
  );
}

function StopBlock({
  stop,
  index,
  colour,
  lo,
  active,
  onHover,
}: {
  stop: Stop;
  index: number;
  colour: string;
  lo: number;
  active: boolean;
  onHover: (id: number | null) => void;
}) {
  const top = (toMin(stop.arrive_hhmm) - lo) * PX_PER_MIN;
  const height = Math.max(stop.dwell_minutes * PX_PER_MIN, 22);

  return (
    <article
      className="absolute inset-x-0 cursor-default rounded-[3px] px-2 py-1 transition-[background-color,box-shadow]"
      style={{
        top,
        height,
        background: active ? "#1D2C28" : "var(--color-paper-raised)",
        boxShadow: active
          ? `inset 3px 0 0 ${colour}, 0 0 0 1px ${colour}55`
          : `inset 3px 0 0 ${colour}`,
      }}
      onMouseEnter={() => onHover(stop.poi_id)}
      onMouseLeave={() => onHover(null)}
    >
      <div className="flex items-baseline gap-1.5">
        <span className="text-[10px] tabular-nums" style={{ color: colour }}>
          {index + 1}
        </span>
        <span className="truncate text-[12px] leading-tight font-medium">{stop.name}</span>
      </div>
      {height > 34 && (
        <p className="mt-0.5 truncate text-[10.5px] text-ink-soft tabular-nums">
          {stop.arrive_hhmm}–{stop.depart_hhmm}
          {stop.quietness !== null && ` · quiet ${Math.round(stop.quietness * 100)}%`}
          {!stop.hours_known && " · hours estimated"}
        </p>
      )}
      {height > 62 && stop.why.length > 0 && (
        <p className="mt-1 line-clamp-2 text-[10.5px] leading-snug text-ink-faint">
          {stop.why.join(" · ")}
        </p>
      )}
      {stop.walk_minutes_from_prev > 0 && (
        <span
          className="absolute -top-[13px] left-2 text-[9.5px] tabular-nums text-ink-faint"
          title={`${stop.walk_metres_from_prev} m on foot`}
        >
          {stop.walk_minutes_from_prev} min walk
        </span>
      )}
    </article>
  );
}
