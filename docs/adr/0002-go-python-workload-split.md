# 2. Split Go and Python by workload character, not by feature

Date: 2026-09-05 · Status: accepted

## Context

Three languages in one system needs a defensible boundary, or it is resume-driven
design. Splitting by feature ("Go owns trips, Python owns search") produces
arbitrary seams. Splitting by workload character does not.

## Decision

> **Go owns the concurrent I/O edge. Python owns the compute.**

| | Go | Python |
|---|---|---|
| WebSocket fan-in/out, thousands of connections | yes | |
| Hot-tier spatial reads (Dragonfly GEOSEARCH) | yes | |
| OSM PBF parse, embedding batch, DB load, cache warm | yes | |
| LLM orchestration, intent extraction, retrieval | | yes |
| OR-Tools solve and the fast repair path | | yes |
| `opening_hours` parsing | | yes (see ADR 0004) |

## Rationale, stated precisely

The common phrasing -- "Python's GIL bottlenecks under connection load" -- is
imprecise and will not survive a probing reviewer. The accurate claim:

1. An asyncio event loop is single-threaded, so framing and serialisation for N
   connections contend for one core.
2. Per-connection Python object overhead is roughly 5-10x Go's.
3. One accidentally-synchronous call blocks *every* connection, not just one.

Point 1 is mitigable with `uvicorn --workers N`, which is exactly why the
benchmark includes a multi-worker Python arm. Beating single-worker Python would
prove nothing.

## Consequences

- Costs a serialisation hop on the cold path and a second deployment artifact.
- The justification is **workload isolation** -- the same argument that justifies
  the Dragonfly hot tier (ADR 0007), proven by the same benchmark. One argument
  supporting two decisions is architecture; two unrelated arguments would be
  scope creep.
- The claim is falsifiable and will be tested. If the benchmark shows multi-worker
  Python closing the gap, that result gets published rather than buried.
