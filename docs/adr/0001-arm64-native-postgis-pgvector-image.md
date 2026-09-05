# 1. Build our own arm64-native PostGIS + pgvector image

Date: 2026-09-05 · Status: accepted

## Context

The system needs PostGIS (geospatial) and pgvector (embeddings) in one database.
No official image ships both, and the development machine is Apple Silicon.

Options evaluated, with live manifest checks:

| option | arm64 | verdict |
|---|---|---|
| `postgis/postgis:17-3.5` | **no, amd64 only** | Runs under Rosetta. 2-5x slower ingest and index builds. |
| `pgvector/pgvector:pg17` | yes | No PostGIS. |
| `imresamu/postgis:...-bundle` | yes, "experimental" | Maintainer labels both the bundle and arm64 support experimental; pulls in ~1GB of extensions we do not use, on a disk-constrained machine. |
| `postgres:17-bookworm` + PGDG apt | **yes** | Chosen. |

## Decision

A 12-line Dockerfile from the official `postgres:17-bookworm` image, installing
PostGIS and pgvector from PGDG apt (which publishes arm64 for bookworm), with
exact versions pinned from a verified build.

## Consequences

- Verified on 2026-09-05: image is `arm64`, PostgreSQL 17.11, PostGIS 3.6.4,
  pgvector 0.8.6. The build pulled `binary-arm64` packages.
- We got PostGIS **3.6.4** rather than the 3.5 originally planned -- PGDG bookworm
  now ships 3.6, so the "3.6 is sid-only" concern no longer applies.
- Version bumps are deliberate: the pins must be edited, and CI asserts they
  still resolve and that both extensions load.
- `make verify` asserts image architecture matches the host, so a regression to
  an emulated image is caught rather than silently tolerated.
