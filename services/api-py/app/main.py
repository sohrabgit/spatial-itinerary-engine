"""FastAPI service: prompt in, routed itinerary out."""

from __future__ import annotations

import os
import time
from contextlib import asynccontextmanager

import structlog
from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware

from app import db, optimize, osrm, retrieval
from app import intent as intent_mod
from app.schemas import Day, Itinerary, ItineraryRequest, Stop

log = structlog.get_logger()


@asynccontextmanager
async def lifespan(_: FastAPI):
    # Fail loudly at startup rather than serving garbage similarity scores.
    await db.assert_embedding_dim()
    yield
    await db.close()


app = FastAPI(title="Flâneur — spatial itinerary engine", lifespan=lifespan)

# The web app runs on a different port, so every browser call is cross-origin
# and fails at preflight without this. Local development origins only -- a
# permissive wildcard here would be a real finding in a portfolio review.
app.add_middleware(
    CORSMiddleware,
    allow_origins=[o.strip() for o in os.getenv(
        "CORS_ORIGINS", "http://localhost:3000,http://localhost:3002"
    ).split(",")],
    allow_methods=["GET", "POST"],
    allow_headers=["Content-Type"],
)


@app.get("/healthz")
async def healthz():
    return {"status": "ok"}


@app.get("/readyz")
async def readyz():
    """What to screenshot when something is wrong."""
    checks: dict[str, str] = {}
    try:
        p = await db.pool()
        await p.fetchval("SELECT 1")
        checks["postgres"] = "ok"
    except Exception as e:
        checks["postgres"] = f"fail: {type(e).__name__}"
    try:
        await osrm.table([(2.3364, 48.8606), (2.3376, 48.8530)])
        checks["osrm"] = "ok"
    except Exception as e:
        checks["osrm"] = f"fail: {type(e).__name__}"
    try:
        from app.embed import embed_query
        await embed_query("healthcheck")
        checks["ollama"] = "ok"
    except Exception as e:
        checks["ollama"] = f"fail: {type(e).__name__}"
    ready = all(v == "ok" for v in checks.values())
    return {"ready": ready, "checks": checks}


def _hhmm_to_min(s: str) -> int:
    h, m = s.split(":")
    return int(h) * 60 + int(m)


@app.post("/itinerary", response_model=Itinerary)
async def build_itinerary(req: ItineraryRequest) -> Itinerary:
    timings: dict[str, int] = {}
    warnings: list[str] = []
    t_all = time.perf_counter()

    ir = await intent_mod.extract(req.prompt, req.model)
    timings["intent_ms"] = ir.latency_ms
    if ir.degraded:
        warnings.append(
            "Could not fully parse the request; using keyword extraction and "
            "semantic search instead."
        )
    if ir.uncertain:
        warnings.append(f"Guessed: {', '.join(ir.uncertain)}")

    it = ir.intent
    start_min = _hhmm_to_min(it.daily_start_hhmm)
    end_min = _hhmm_to_min(it.daily_end_hhmm)
    # Wednesday-anchored for a reproducible demo; a real trip would take a date.
    dows = [(2 + i) % 7 for i in range(it.days)]

    # Radius scales with pace and days: a packed 3-day trip legitimately ranges
    # further than a relaxed afternoon.
    radius = {"relaxed": 2000, "balanced": 3000, "packed": 4500}[it.pace]

    t0 = time.perf_counter()
    res = await retrieval.retrieve(
        it, lat=req.start_lat, lon=req.start_lon, radius_m=radius,
        # ~9 stops/day survive the solve, and group quotas thin the pool
        # further, so 12/day left the last day with scraps. 20 gives the solver
        # real choice on every day rather than only the first.
        limit=max(30, it.days * 20), dow=dows[0], at_minute=start_min + 120,
    )
    timings["retrieval_ms"] = int((time.perf_counter() - t0) * 1000)
    if not res.candidates:
        raise HTTPException(422, "No POIs matched. Try widening the request.")

    cands = res.candidates
    hours = await retrieval.fetch_hours([c.poi_id for c in cands], dows)

    # Route on snapped points; display the originals.
    t0 = time.perf_counter()
    coords = [(req.start_lon, req.start_lat)] + [(c.snap_lon, c.snap_lat) for c in cands]
    durations, distances = await osrm.table(coords)
    timings["matrix_ms"] = int((time.perf_counter() - t0) * 1000)

    sol = optimize.solve(
        cands, durations, distances, hours,
        days=it.days, day_start_min=start_min, day_end_min=end_min,
        max_walk_m_per_day=int(it.max_walk_km_per_day * 1000), dows=dows,
    )
    timings["solve_ms"] = sol.solve_ms
    if sol.status not in {"ROUTING_SUCCESS", "ROUTING_OPTIMAL"}:
        warnings.append(f"Solver status: {sol.status}")

    days_out: list[Day] = []
    for d in range(it.days):
        day_stops = [s for s in sol.stops if s.day == d]
        day_stops.sort(key=lambda s: s.arrive_min)
        stops = [
            Stop(
                poi_id=s.candidate.poi_id, name=s.candidate.name,
                category=s.candidate.category, lat=s.candidate.lat, lon=s.candidate.lon,
                arrive_hhmm=optimize._hhmm(s.arrive_min),
                depart_hhmm=optimize._hhmm(s.depart_min),
                dwell_minutes=s.candidate.dwell_minutes,
                walk_minutes_from_prev=round(s.walk_seconds_from_prev / 60),
                walk_metres_from_prev=s.walk_metres_from_prev,
                quietness=s.candidate.quietness, hours_known=s.candidate.hours_known,
                why=s.candidate.why,
            )
            for s in day_stops
        ]
        poly = ""
        if len(day_stops) >= 2:
            leg = [(req.start_lon, req.start_lat)] + [
                (s.candidate.snap_lon, s.candidate.snap_lat) for s in day_stops
            ]
            poly, _dist, _dur = await osrm.route_polyline(leg)
        days_out.append(Day(
            day_index=d, stops=stops,
            total_walk_metres=sum(s.walk_metres_from_prev for s in day_stops),
            total_walk_minutes=round(sum(s.walk_seconds_from_prev for s in day_stops) / 60),
            polyline=poly,
        ))

    infeasible = optimize.repair(sol.stops, hours, end_min)
    warnings.extend(infeasible)

    timings["total_ms"] = int((time.perf_counter() - t_all) * 1000)
    log.info("itinerary", prompt=req.prompt[:80], days=it.days,
             candidates=len(cands), stops=len(sol.stops), **timings)

    return Itinerary(
        intent=it, days=days_out, candidates_considered=res.considered,
        query_class=res.query_class, solver_status=sol.status,
        objective=sol.objective, warnings=warnings, timings_ms=timings,
    )
