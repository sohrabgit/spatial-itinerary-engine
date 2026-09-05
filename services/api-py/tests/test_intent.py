import json

import httpx
import respx

from app.intent import extract
from app.schemas import TravelIntent


def _ok(payload: dict | str) -> httpx.Response:
    content = payload if isinstance(payload, str) else json.dumps(payload)
    return httpx.Response(200, json={"message": {"content": content}})


@respx.mock
async def test_prepass_overrides_the_model():
    # The model says 5 days; the prompt plainly says 3. Deterministic wins.
    respx.post("http://localhost:11434/api/chat").mock(
        return_value=_ok({**TravelIntent().model_dump(), "days": 5, "city": "Rome"})
    )
    r = await extract("3-day historic walk in Paris with quiet cafes")
    assert r.intent.days == 3, "regex duration must beat the model"
    assert r.intent.city == "Paris"
    assert r.valid and not r.degraded


@respx.mock
async def test_invalid_json_retries_then_degrades_without_failing():
    respx.post("http://localhost:11434/api/chat").mock(
        return_value=httpx.Response(200, json={"message": {"content": "not json at all"}})
    )
    r = await extract("3-day trip to Paris with quiet cafes")
    assert r.degraded and r.retries >= 1
    # Degradation must still produce a usable intent, not an exception.
    assert r.intent.days == 3
    assert r.intent.free_text_residual != ""
    assert "themes" not in r.uncertain or r.intent.themes


@respx.mock
async def test_transport_error_degrades_gracefully():
    respx.post("http://localhost:11434/api/chat").mock(side_effect=httpx.ConnectError("down"))
    r = await extract("2 days in Paris, art and food")
    assert r.degraded
    assert r.intent.days == 2
    assert "art" in r.intent.themes and "food" in r.intent.themes
    assert r.intent.free_text_residual != ""
