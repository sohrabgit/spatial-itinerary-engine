"""OSRM routing client."""

from __future__ import annotations

import httpx

from app.config import settings


async def table(coords: list[tuple[float, float]]) -> tuple[list[list[float]], list[list[float]]]:
    """Duration (s) and distance (m) matrices for walking.

    Coordinates must be the OSRM-snapped points, not display centroids: routing
    from a building centroid can be 100m+ off the graph and corrupts every cell.
    """
    locs = ";".join(f"{lon:.6f},{lat:.6f}" for lon, lat in coords)
    url = f"{settings.osrm_base_url}/table/v1/foot/{locs}?annotations=duration,distance"
    async with httpx.AsyncClient(timeout=60.0) as c:
        r = await c.get(url)
        r.raise_for_status()
        data = r.json()
    if data.get("code") != "Ok":
        raise RuntimeError(f"osrm table: {data.get('code')} {data.get('message', '')}")
    return data["durations"], data["distances"]


async def route_polyline(coords: list[tuple[float, float]]) -> tuple[str, float, float]:
    """Encoded polyline plus total distance/duration for a fixed stop order."""
    if len(coords) < 2:
        return "", 0.0, 0.0
    locs = ";".join(f"{lon:.6f},{lat:.6f}" for lon, lat in coords)
    url = f"{settings.osrm_base_url}/route/v1/foot/{locs}?overview=full&geometries=polyline6"
    async with httpx.AsyncClient(timeout=60.0) as c:
        r = await c.get(url)
        r.raise_for_status()
        data = r.json()
    if data.get("code") != "Ok" or not data.get("routes"):
        return "", 0.0, 0.0
    rt = data["routes"][0]
    return rt.get("geometry", ""), rt.get("distance", 0.0), rt.get("duration", 0.0)
