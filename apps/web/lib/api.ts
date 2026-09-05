import type { Itinerary } from "./types";

const API = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8000";

export async function buildItinerary(prompt: string): Promise<Itinerary> {
  const res = await fetch(`${API}/itinerary`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ prompt }),
  });
  if (!res.ok) {
    const detail = await res.text().catch(() => "");
    throw new Error(
      res.status === 422
        ? "No places matched that. Try a broader request."
        : `The planner is unavailable (${res.status}). ${detail.slice(0, 120)}`,
    );
  }
  return res.json();
}
