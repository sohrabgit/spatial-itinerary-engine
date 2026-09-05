import pytest

from app.prepass import run


@pytest.mark.parametrize(
    "prompt,key,expected",
    [
        ("3-day historic walk in Paris with quiet cafes and minimal transit", "days", 3),
        ("three day trip to Paris", "days", 3),
        ("a weekend in Paris", "days", 2),
        ("day trip to Paris", "days", 1),
        ("2 nights in Paris", "days", 2),
        ("Paris for a day", "days", 1),
    ],
)
def test_duration_is_never_left_to_the_model(prompt, key, expected):
    assert run(prompt).fields.get(key) == expected


def test_headline_prompt_extracts_everything_deterministic():
    f = run("3-day historic walk in Paris with quiet cafes and minimal transit").fields
    assert f["days"] == 3
    assert f["city"] == "Paris"
    assert "historic" in f["themes"] and "cafe" in f["themes"]
    assert "quiet" in f["ambience"]
    assert f["transit_tolerance"] == "minimal"


def test_start_time():
    assert run("start at 8am, walking tour").fields["daily_start_hhmm"] == "08:00"
    assert run("from 2pm onwards").fields["daily_start_hhmm"] == "14:00"
    assert run("starting at 10:30").fields["daily_start_hhmm"] == "10:30"


def test_wifi_and_accessibility():
    assert run("cafe with wifi to work from").fields["requires_wifi"] is True
    assert run("step-free accessible route").fields["accessibility_required"] is True
    assert "requires_wifi" not in run("a nice walk").fields


def test_evidence_is_recorded():
    r = run("3-day trip")
    assert "days" in r.evidence and "3-day" in r.evidence["days"]


def test_no_false_positives_on_empty_prompt():
    assert run("hello").fields == {}
