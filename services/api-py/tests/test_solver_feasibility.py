"""Feasibility is a correctness test wearing an eval's clothing.

The plan's headline metric is "100% of generated itineraries are feasible":
every stop open on arrival, walking within budget, no time-window violation.
Anything less is a solver bug, not a quality issue -- so it lives in the test
suite, not only in the eval harness.
"""

from app.optimize import solve
from app.retrieval import Candidate


def _cand(poi_id: int, cat: str, dwell: int, score: float = 0.8) -> Candidate:
    return Candidate(
        poi_id=poi_id, name=f"POI {poi_id}", category=cat,
        lat=48.86, lon=2.34, snap_lat=48.86, snap_lon=2.34,
        dwell_minutes=dwell, quietness=0.5, touristiness=0.5, popularity=0.5,
        has_wifi=None, hours_known=True, score=score, why=[],
    )


def _uniform_matrix(n: int, seconds: int, metres: int):
    dur = [[0 if i == j else seconds for j in range(n)] for i in range(n)]
    dist = [[0 if i == j else metres for j in range(n)] for i in range(n)]
    return dur, dist


def test_every_stop_is_open_on_arrival():
    cands = [_cand(i, "museum", 60) for i in range(1, 7)]
    dur, dist = _uniform_matrix(len(cands) + 1, 300, 400)
    # Half the POIs are morning-only, half afternoon-only.
    hours = {i: [(540, 720)] if i % 2 else [(780, 1080)] for i in range(1, 7)}

    r = solve(cands, dur, dist, hours, days=2, day_start_min=540, day_end_min=1140,
              max_walk_m_per_day=8000, dows=[2, 3])

    assert r.stops, "solver returned nothing"
    for s in r.stops:
        windows = hours[s.candidate.poi_id]
        ok = any(o <= s.arrive_min and s.depart_min <= c for o, c in windows)
        assert ok, (
            f"{s.candidate.name} arrives {s.arrive_min} departs {s.depart_min}, "
            f"open {windows} -- infeasible itinerary"
        )


def test_walking_budget_is_respected_per_day():
    cands = [_cand(i, "museum", 30) for i in range(1, 11)]
    dur, dist = _uniform_matrix(len(cands) + 1, 200, 900)
    hours = {i: [(0, 1440)] for i in range(1, 11)}

    budget = 3000
    r = solve(cands, dur, dist, hours, days=2, day_start_min=540, day_end_min=1140,
              max_walk_m_per_day=budget, dows=[2, 3])

    for day in range(2):
        walked = sum(s.walk_metres_from_prev for s in r.stops if s.day == day)
        assert walked <= budget, f"day {day} walked {walked}m over a {budget}m budget"


def test_stops_never_exceed_the_daily_end_time():
    cands = [_cand(i, "museum", 90) for i in range(1, 9)]
    dur, dist = _uniform_matrix(len(cands) + 1, 600, 800)
    hours = {i: [(0, 1440)] for i in range(1, 9)}

    end = 1080  # 18:00
    r = solve(cands, dur, dist, hours, days=1, day_start_min=540, day_end_min=end,
              max_walk_m_per_day=20000, dows=[2])
    for s in r.stops:
        assert s.depart_min <= end, f"{s.candidate.name} departs after closing time"


def test_category_cap_prevents_a_day_of_only_cafes():
    # The cap exists because the unconstrained solver produced a five-cafe day.
    cands = [_cand(i, "cafe", 45) for i in range(1, 9)]
    dur, dist = _uniform_matrix(len(cands) + 1, 120, 200)
    hours = {i: [(0, 1440)] for i in range(1, 9)}

    r = solve(cands, dur, dist, hours, days=1, day_start_min=540, day_end_min=1140,
              max_walk_m_per_day=20000, dows=[2])
    cafes = sum(1 for s in r.stops if s.candidate.category == "cafe")
    assert cafes <= 2, f"{cafes} cafes in one day; the food cap is not binding"


def test_empty_candidate_set_does_not_crash():
    dur, dist = _uniform_matrix(1, 0, 0)
    r = solve([], dur, dist, {}, days=1, day_start_min=540, day_end_min=1140,
              max_walk_m_per_day=5000, dows=[2])
    assert r.stops == []
