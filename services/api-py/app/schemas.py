"""Wire and internal schemas.

The single most important constraint here: TravelIntent is FLAT.

Measured behaviour for sub-4B models is 2-3% structured-output non-compliance on
flat schemas, rising to 68-69% once ``$defs`` appear. Nested models, ``$ref``,
``anyOf``/``oneOf`` and arrays-of-objects all introduce ``$defs`` in Pydantic's
generated JSON Schema. So: primitives, ``Literal`` enums, and arrays of
primitives only. Anything that wants to be nested gets flattened into parallel
fields instead. There is a test asserting no ``$defs`` appear.
"""

from typing import Literal

from pydantic import BaseModel, Field

Pace = Literal["relaxed", "balanced", "packed"]
TransitTolerance = Literal["none", "minimal", "normal"]
BudgetLevel = Literal["low", "mid", "high"]


class TravelIntent(BaseModel):
    """What the user asked for, normalised. Flat by construction."""

    city: str = Field(default="Paris")
    days: int = Field(default=1, ge=1, le=14)
    daily_start_hhmm: str = Field(default="09:30")
    daily_end_hhmm: str = Field(default="19:00")
    themes: list[str] = Field(default_factory=list)
    must_include: list[str] = Field(default_factory=list)
    avoid: list[str] = Field(default_factory=list)
    ambience: list[str] = Field(default_factory=list)
    pace: Pace = "balanced"
    transit_tolerance: TransitTolerance = "normal"
    max_walk_km_per_day: float = Field(default=6.0, ge=0.5, le=30.0)
    requires_wifi: bool = False
    accessibility_required: bool = False
    budget_level: BudgetLevel = "mid"
    party_size: int = Field(default=1, ge=1, le=20)
    # Whatever the deterministic pass and the model could not place. Fed to
    # semantic retrieval verbatim so nothing the user said is silently dropped.
    free_text_residual: str = ""


class Stop(BaseModel):
    poi_id: int
    name: str
    category: str
    lat: float
    lon: float
    arrive_hhmm: str
    depart_hhmm: str
    dwell_minutes: int
    walk_minutes_from_prev: int
    walk_metres_from_prev: int
    quietness: float | None = None
    hours_known: bool = True
    why: list[str] = Field(default_factory=list)


class Day(BaseModel):
    day_index: int
    stops: list[Stop]
    total_walk_metres: int
    total_walk_minutes: int
    polyline: str = ""


class Itinerary(BaseModel):
    intent: TravelIntent
    days: list[Day]
    candidates_considered: int
    query_class: str
    solver_status: str
    objective: int
    warnings: list[str] = Field(default_factory=list)
    timings_ms: dict[str, int] = Field(default_factory=dict)


class ItineraryRequest(BaseModel):
    prompt: str = Field(min_length=1, max_length=2000)
    start_lat: float = 48.8606
    start_lon: float = 2.3364
    model: str | None = None
