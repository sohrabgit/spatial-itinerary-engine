"""Deterministic extraction, run BEFORE the LLM.

A 3B model doing arithmetic or date maths is the single largest source of
confident nonsense in this pipeline. "3-day" becoming days=2, "next weekend"
becoming a hallucinated date, "a couple of hours" becoming 2 days. All of it is
regex-solvable, none of it needs a model.

Whatever this pass extracts is treated as ground truth and overrides the LLM.
Everything it cannot place is left for the model.
"""

from __future__ import annotations

import re
import unicodedata
from dataclasses import dataclass, field

# Cities the corpus actually covers. Anything else is a hard error upstream
# rather than a silently empty itinerary.
KNOWN_CITIES = {"paris": "Paris"}

_NUM_WORDS = {
    "a": 1, "an": 1, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
    "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
}

_DAYS_RE = re.compile(
    r"\b(?P<n>\d{1,2}|" + "|".join(_NUM_WORDS) + r")[\s-]*(?:day|days|nights?|jour)\b",
    re.IGNORECASE,
)
_WEEKEND_RE = re.compile(r"\bweekend\b", re.IGNORECASE)
_DAYTRIP_RE = re.compile(
    r"\b(day\s?trip|one\s?day|single\s?day|just\s+a\s+day)\b", re.IGNORECASE
)
_TIME_RE = re.compile(
    r"\b(?:from|start(?:ing)?(?:\s+at)?)\s+"
    r"(?P<h>\d{1,2})(?::(?P<m>\d{2}))?\s*(?P<ap>am|pm)?\b",
    re.IGNORECASE,
)

# Theme and ambience vocabulary. Deliberately small and explicit: these map onto
# the categories the corpus actually has, so a match is meaningful.
_THEMES = {
    "historic": ["historic", "history", "historical", "heritage", "medieval",
                 "ancient", "old town"],
    "art": ["art", "gallery", "galleries", "museum", "museums", "painting",
            "impressionist", "sculpture"],
    "food": ["food", "eat", "eating", "culinary", "gastronomy", "restaurant",
             "dining", "foodie"],
    "cafe": ["cafe", "cafes", "coffee", "espresso"],
    "nature": ["park", "parks", "garden", "gardens", "green", "nature", "outdoors"],
    "nightlife": ["nightlife", "bar", "bars", "drinks", "cocktail", "pub"],
    "architecture": ["architecture", "architectural", "building", "cathedral", "church"],
    "shopping": ["shopping", "shop", "boutique", "market", "markets"],
    "views": ["view", "views", "viewpoint", "panorama", "skyline", "rooftop"],
}
_AMBIENCE = {
    "quiet": ["quiet", "peaceful", "calm", "tranquil", "relaxed", "serene",
              "low-key", "chill"],
    "lively": ["lively", "buzzy", "busy", "vibrant", "bustling", "energetic"],
    "local": ["local", "locals", "authentic", "off the beaten", "non-touristy", "hidden"],
    "touristy": ["iconic", "must-see", "famous", "landmark", "classic"],
    "romantic": ["romantic", "date", "intimate"],
}
_PACE = {
    "relaxed": ["relaxed", "slow", "leisurely", "easy", "unhurried", "gentle"],
    "packed": ["packed", "fast", "intense", "see everything", "as much as",
               "maximise", "maximize"],
}
_TRANSIT = {
    "none": ["walk only", "walking only", "no transit", "no metro", "on foot",
             "entirely on foot"],
    "minimal": ["minimal transit", "little transit", "avoid transit",
                "avoid the metro", "mostly walking", "minimal metro"],
}
_WIFI = ["wifi", "wi-fi", "wireless", "work from", "laptop", "remote work"]
_ACCESS = ["wheelchair", "accessible", "step-free", "mobility"]


@dataclass
class PrepassResult:
    """Fields the deterministic pass is confident about, plus its evidence."""

    fields: dict[str, object] = field(default_factory=dict)
    evidence: dict[str, str] = field(default_factory=dict)

    def set(self, key: str, value: object, because: str) -> None:
        self.fields[key] = value
        self.evidence[key] = because


def _word_to_int(tok: str) -> int | None:
    tok = tok.lower()
    if tok.isdigit():
        return int(tok)
    return _NUM_WORDS.get(tok)


def _fold(text: str) -> str:
    """Strip accents before keyword matching.

    French prompts naturally write "cafés", "musée", "château". Matching against
    an unaccented keyword list silently dropped the theme -- "quiet cafés"
    extracted no cafe theme at all, and the itinerary came back without one.
    """
    return "".join(
        c for c in unicodedata.normalize("NFD", text.lower())
        if unicodedata.category(c) != "Mn"
    )


def run(prompt: str) -> PrepassResult:
    r = PrepassResult()
    low = _fold(prompt)

    for key, canonical in KNOWN_CITIES.items():
        if key in low:
            r.set("city", canonical, f"matched city name {key!r}")
            break

    # Duration. Explicit "N days" wins; then day-trip; then weekend.
    m = _DAYS_RE.search(prompt)
    if m and (n := _word_to_int(m.group("n"))) and 1 <= n <= 14:
        r.set("days", n, f"matched {m.group(0)!r}")
    if "days" not in r.fields:
        if _DAYTRIP_RE.search(low):
            r.set("days", 1, "matched a day-trip phrase")
        elif _WEEKEND_RE.search(low):
            r.set("days", 2, "'weekend' means 2 days")

    if m := _TIME_RE.search(prompt):
        h = int(m.group("h"))
        mi = int(m.group("m") or 0)
        ap = (m.group("ap") or "").lower()
        if ap == "pm" and h < 12:
            h += 12
        elif ap == "am" and h == 12:
            h = 0
        if 0 <= h <= 23:
            r.set("daily_start_hhmm", f"{h:02d}:{mi:02d}", f"matched {m.group(0)!r}")

    themes = [k for k, words in _THEMES.items() if any(w in low for w in words)]
    if themes:
        r.set("themes", themes, "keyword match")
    ambience = [k for k, words in _AMBIENCE.items() if any(w in low for w in words)]
    if ambience:
        r.set("ambience", ambience, "keyword match")

    for pace, words in _PACE.items():
        if any(w in low for w in words):
            r.set("pace", pace, "keyword match")
            break
    for tol, words in _TRANSIT.items():
        if any(w in low for w in words):
            r.set("transit_tolerance", tol, "keyword match")
            break

    if any(w in low for w in _WIFI):
        r.set("requires_wifi", True, "keyword match")
    if any(w in low for w in _ACCESS):
        r.set("accessibility_required", True, "keyword match")

    return r
