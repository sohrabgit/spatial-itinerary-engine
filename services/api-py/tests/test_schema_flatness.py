"""The load-bearing constraint of the whole AI layer.

If TravelIntent ever grows a nested model, Pydantic emits ``$defs`` and
structured-output compliance on a 3B model collapses from ~97% to ~32%. This
test is what stops that happening by accident.
"""

import json

from app.schemas import TravelIntent


def test_intent_schema_has_no_defs():
    schema = TravelIntent.model_json_schema()
    blob = json.dumps(schema)
    assert "$defs" not in blob, "TravelIntent must stay flat: $defs collapses 3B-model compliance"
    assert "$ref" not in blob, "TravelIntent must stay flat: $ref collapses 3B-model compliance"


def test_intent_properties_are_primitives_or_primitive_arrays():
    schema = TravelIntent.model_json_schema()
    for name, prop in schema["properties"].items():
        if "enum" in prop or "anyOf" in prop:
            continue
        t = prop.get("type")
        assert t in {"string", "integer", "number", "boolean", "array"}, f"{name}: {t}"
        if t == "array":
            assert prop["items"].get("type") in {"string", "integer", "number"}, name


def test_defaults_make_a_usable_intent():
    # Degradation path: if the LLM fails entirely, defaults must still produce
    # something the solver can run on.
    i = TravelIntent()
    assert i.days >= 1 and i.city and i.daily_start_hhmm < i.daily_end_hhmm
