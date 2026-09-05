"""Prompt -> TravelIntent.

Order matters and is the whole design:

1. Deterministic pre-pass. Owns dates, durations, times, and keyword themes.
2. LLM, temperature 0, schema-constrained decoding, schema also in the prompt.
3. Pydantic validation; on failure, one retry with the validation error fed back.
4. Degrade, never fail. If the model is unusable we still return an intent built
   from the pre-pass plus defaults, with the full prompt in free_text_residual
   so semantic retrieval still sees everything the user said.

The user always gets an itinerary. A model failure costs precision, not the
request.
"""

from __future__ import annotations

import json
import time
from dataclasses import dataclass

import httpx
from pydantic import ValidationError

from app import prepass
from app.config import settings
from app.schemas import TravelIntent

SYSTEM = """You extract structured travel intent from a user's request.
Return ONLY JSON matching this schema. Do not add commentary.

{schema}

Rules:
- Never invent a city that is not mentioned.
- themes/ambience: short lowercase words, at most 5 each.
- must_include: specific named places the user explicitly asked for.
- avoid: things the user explicitly does not want.
- free_text_residual: anything meaningful you could not place in a field.
"""

FEWSHOT = [
    (
        "3-day historic walk in Paris with quiet cafes and minimal transit",
        {
            "city": "Paris", "days": 3, "daily_start_hhmm": "09:30", "daily_end_hhmm": "19:00",
            "themes": ["historic", "cafe"], "must_include": [], "avoid": [],
            "ambience": ["quiet"], "pace": "relaxed", "transit_tolerance": "minimal",
            "max_walk_km_per_day": 6.0, "requires_wifi": False, "accessibility_required": False,
            "budget_level": "mid", "party_size": 1, "free_text_residual": "",
        },
    ),
    (
        "one packed day in Paris, must see the Louvre, we hate crowds, 2 of us",
        {
            "city": "Paris", "days": 1, "daily_start_hhmm": "09:00", "daily_end_hhmm": "20:00",
            "themes": ["art"], "must_include": ["Louvre"], "avoid": ["crowds"],
            "ambience": ["local"], "pace": "packed", "transit_tolerance": "normal",
            "max_walk_km_per_day": 9.0, "requires_wifi": False, "accessibility_required": False,
            "budget_level": "mid", "party_size": 2, "free_text_residual": "",
        },
    ),
]


@dataclass
class IntentResult:
    intent: TravelIntent
    valid: bool          # did the model produce a schema-valid object?
    retries: int
    degraded: bool       # did we fall back to pre-pass + defaults?
    latency_ms: int
    raw: str
    uncertain: list[str] # fields the user should be told we guessed


async def extract(prompt: str, model: str | None = None) -> IntentResult:
    t0 = time.perf_counter()
    pre = prepass.run(prompt)
    model = model or settings.ollama_chat_model

    schema = TravelIntent.model_json_schema()
    system = SYSTEM.format(schema=json.dumps(schema, indent=None))
    messages = [{"role": "system", "content": system}]
    for user, out in FEWSHOT:
        messages.append({"role": "user", "content": user})
        messages.append({"role": "assistant", "content": json.dumps(out)})
    messages.append({"role": "user", "content": prompt})

    raw = ""
    retries = 0
    parsed: TravelIntent | None = None
    last_error = ""

    async with httpx.AsyncClient(timeout=90.0) as client:
        for attempt in range(2):
            try:
                resp = await client.post(
                    f"{settings.ollama_base_url}/api/chat",
                    json={
                        "model": model,
                        "stream": False,
                        "format": schema,           # token-level constrained decoding
                        "options": {"temperature": 0, "num_predict": 500},
                        "messages": messages,
                    },
                )
                resp.raise_for_status()
                raw = resp.json().get("message", {}).get("content", "")
                parsed = TravelIntent.model_validate_json(raw)
                break
            except ValidationError as e:
                last_error = str(e)[:400]
                retries += 1
                # Feed the failure back rather than retrying blind.
                messages.append({"role": "assistant", "content": raw})
                messages.append({
                    "role": "user",
                    "content": f"That failed validation: {last_error}\nReturn corrected JSON only.",
                })
            except (httpx.HTTPError, json.JSONDecodeError) as e:
                last_error = f"{type(e).__name__}: {e}"
                retries += 1
                if attempt == 1:
                    break

    degraded = parsed is None
    intent = parsed or TravelIntent()

    # The deterministic pass always wins. It is not a fallback -- it is the
    # authority on everything it claims.
    overrides = pre.fields
    if overrides:
        intent = intent.model_copy(update=overrides)

    uncertain: list[str] = []
    if degraded:
        # Nothing was structurally understood, so push the whole prompt into
        # semantic retrieval and say which fields are guesses.
        intent = intent.model_copy(update={"free_text_residual": prompt})
        guessable = ("themes", "ambience", "pace", "budget_level")
        uncertain = [f for f in guessable if f not in overrides]

    return IntentResult(
        intent=intent,
        valid=not degraded,
        retries=retries,
        degraded=degraded,
        latency_ms=int((time.perf_counter() - t0) * 1000),
        raw=raw or last_error,
        uncertain=uncertain,
    )
