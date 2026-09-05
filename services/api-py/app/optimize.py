"""Itinerary construction.

This is a Team Orienteering Problem with Time Windows (TOPTW), not a VRPTW.
In a VRPTW every customer must be served; here there are 30-60 candidates, a
handful of days, and the whole point is choosing a profitable SUBSET subject to
time. Naming it correctly is the difference between using the routing library
and understanding which problem you have.

OR-Tools models optional visits through disjunctions with penalties, which is
exactly the TOPTW mechanism.
"""

from __future__ import annotations

import time
from dataclasses import dataclass

from ortools.constraint_solver import pywrapcp, routing_enums_pb2

from app.config import settings
from app.retrieval import Candidate

# Penalty for skipping a candidate, in seconds of walking, scaled by relevance.
# 3000s means a top-relevance POI is worth ~50 minutes of detour while a
# marginal one is worth ~10 -- and that ratio IS the walking-preference knob a
# UI slider would expose. It only works because arc cost excludes service time.
# In seconds, so it is directly comparable to travel + dwell. A top stop
# (score ~0.9) is worth ~90 minutes of the day; a marginal one (~0.3) about
# 30. That ratio is the walking-vs-seeing knob a UI slider would expose.
PENALTY_SCALE = 6000


@dataclass
class PlannedStop:
    candidate: Candidate
    day: int
    arrive_min: int
    depart_min: int
    walk_seconds_from_prev: int
    walk_metres_from_prev: int


@dataclass
class SolveResult:
    stops: list[PlannedStop]
    status: str
    objective: int
    dropped: int
    solve_ms: int


def _hhmm(minutes: int) -> str:
    return f"{(minutes // 60) % 24:02d}:{minutes % 60:02d}"


def solve(
    candidates: list[Candidate],
    durations: list[list[float]],
    distances: list[list[float]],
    hours: dict[int, list[tuple[int, int]]],
    days: int,
    day_start_min: int,
    day_end_min: int,
    max_walk_m_per_day: int,
    dows: list[int],
) -> SolveResult:
    """Node 0 is the depot (trip start). Nodes 1..n are candidates."""
    t0 = time.perf_counter()
    n = len(candidates) + 1

    manager = pywrapcp.RoutingIndexManager(n, days, 0)
    routing = pywrapcp.RoutingModel(manager)

    # Two callbacks, deliberately different -- this distinction is the whole
    # difference between an itinerary and an empty one.
    #
    # The TIME dimension must include service time, or arrival times are wrong.
    # The ARC COST must NOT, or the solver compares "5400s to visit a museum"
    # against "600s penalty to skip it" and rationally skips everything. First
    # attempt did exactly that: 36 candidates, 0 stops, ROUTING_SUCCESS.
    def travel_plus_service(from_index: int, to_index: int) -> int:  # noqa: E306
        i = manager.IndexToNode(from_index)
        j = manager.IndexToNode(to_index)
        service = 0 if i == 0 else candidates[i - 1].dwell_minutes * 60
        return int(durations[i][j]) + service

    # Service time belongs in the arc cost after all -- otherwise a 15-minute
    # wall plaque is nearly free to insert and the solver packs days with cheap
    # low-value stops instead of one great museum. It only works once PENALTY
    # is scaled in the same units (seconds) and large enough that a genuinely
    # good stop is worth the time it consumes.
    cost_cb = routing.RegisterTransitCallback(travel_plus_service)
    routing.SetArcCostEvaluatorOfAllVehicles(cost_cb)

    time_cb = routing.RegisterTransitCallback(travel_plus_service)
    horizon = day_end_min * 60
    routing.AddDimension(time_cb, horizon, horizon, False, "Time")
    time_dim = routing.GetDimensionOrDie("Time")

    # Walking budget as its own dimension, so "minimal transit" is a real
    # constraint rather than a scoring nudge.
    def dist_cb(from_index: int, to_index: int) -> int:
        return int(distances[manager.IndexToNode(from_index)][manager.IndexToNode(to_index)])

    dist_idx = routing.RegisterTransitCallback(dist_cb)
    routing.AddDimensionWithVehicleCapacity(
        dist_idx, 0, [max_walk_m_per_day] * days, True, "Walk"
    )

    # Per-day category caps. Without these the solver produced a day with five
    # cafes, which is optimal against the objective and useless as a day out.
    # A count dimension is the cheapest way to say "at most N of these".
    from app.retrieval import CATEGORY_GROUP

    for group, cap in (("food", 2), ("nature", 2)):
        def counter(from_index: int, _grp=group) -> int:
            i = manager.IndexToNode(from_index)
            if i == 0:
                return 0
            return 1 if CATEGORY_GROUP.get(candidates[i - 1].category) == _grp else 0

        cb = routing.RegisterUnaryTransitCallback(counter)
        routing.AddDimensionWithVehicleCapacity(cb, 0, [cap] * days, True, f"Count_{group}")

    for v in range(days):
        time_dim.CumulVar(routing.Start(v)).SetRange(day_start_min * 60, day_start_min * 60)
        time_dim.CumulVar(routing.End(v)).SetRange(day_start_min * 60, day_end_min * 60)

    # Opening hours become per-node time windows. A candidate open on none of
    # the trip's days is simply left unconstrained-but-unvisitable via a very
    # tight window; the disjunction then drops it at no cost to feasibility.
    for k, c in enumerate(candidates, start=1):
        idx = manager.NodeToIndex(k)
        windows = hours.get(c.poi_id, [])
        # Union across the trip's weekdays, intersected with the daily bounds.
        opens = [max(o, day_start_min) for (o, _cl) in windows] or [day_start_min]
        closes = [min(cl, day_end_min) for (_o, cl) in windows] or [day_end_min]
        lo = min(opens) * 60
        hi = max(closes) * 60 - c.dwell_minutes * 60
        if hi < lo:
            hi = lo
        time_dim.CumulVar(idx).SetRange(lo, hi)

        # Optional visit. Higher relevance = higher penalty for skipping.
        penalty = int(PENALTY_SCALE * max(c.score, 0.01))
        routing.AddDisjunction([idx], penalty)

    params = pywrapcp.DefaultRoutingSearchParameters()
    params.first_solution_strategy = (
        routing_enums_pb2.FirstSolutionStrategy.PARALLEL_CHEAPEST_INSERTION
    )
    params.local_search_metaheuristic = (
        routing_enums_pb2.LocalSearchMetaheuristic.GUIDED_LOCAL_SEARCH
    )
    params.time_limit.FromSeconds(settings.solver_seconds)
    params.log_search = False

    sol = routing.SolveWithParameters(params)
    solve_ms = int((time.perf_counter() - t0) * 1000)
    if sol is None:
        return SolveResult([], "NO_SOLUTION", 0, len(candidates), solve_ms)

    stops: list[PlannedStop] = []
    visited = 0
    for v in range(days):
        idx = routing.Start(v)
        prev_node = 0
        while not routing.IsEnd(idx):
            node = manager.IndexToNode(idx)
            if node != 0:
                arrive = sol.Value(time_dim.CumulVar(idx)) // 60
                c = candidates[node - 1]
                stops.append(PlannedStop(
                    candidate=c, day=v, arrive_min=arrive,
                    depart_min=arrive + c.dwell_minutes,
                    walk_seconds_from_prev=int(durations[prev_node][node]),
                    walk_metres_from_prev=int(distances[prev_node][node]),
                ))
                visited += 1
                prev_node = node
            idx = sol.Value(routing.NextVar(idx))

    # OR-Tools 9.15: RoutingSearchStatus is a message wrapping a nested enum
    # named Value, not a bare enum and not a RoutingModel attribute.
    try:
        status = routing_enums_pb2.RoutingSearchStatus.Value.Name(routing.status())
    except ValueError:
        status = str(routing.status())
    return SolveResult(
        stops=stops,
        status=status,
        objective=sol.ObjectiveValue(),
        dropped=len(candidates) - visited,
        solve_ms=solve_ms,
    )


def repair(stops: list[PlannedStop], hours: dict[int, list[tuple[int, int]]],
           day_end_min: int) -> list[str]:
    """Fast feasibility re-check for interactive reordering.

    O(n), no solver, ~1ms. This is the T1 tier: a dragged stop needs instant
    feedback, not a 3-second re-solve. Custom algorithm work belongs here rather
    than in a hand-rolled metaheuristic that would only be a worse OR-Tools.
    """
    problems: list[str] = []
    for s in stops:
        windows = hours.get(s.candidate.poi_id, [])
        if not windows:
            continue
        if not any(o <= s.arrive_min and s.depart_min <= cl for o, cl in windows):
            closes = max((cl for _o, cl in windows), default=day_end_min)
            problems.append(
                f"{s.candidate.name}: closes at {_hhmm(closes)}, "
                f"you'd arrive {_hhmm(s.arrive_min)}"
            )
    return problems
