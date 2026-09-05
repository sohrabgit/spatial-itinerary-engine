# 3. Run Ollama natively on the host, not in Docker

Date: 2026-09-05 · Status: accepted

## Context

The zero-cloud mandate means self-hosted inference: `nomic-embed-text` for
embeddings and a small chat model for intent extraction. The obvious move is a
compose service, keeping the stack self-contained.

Docker Desktop on macOS has **no Metal GPU passthrough**. A containerised model
is CPU-only. The ingestion pipeline must embed 25k-150k POIs at least once, and
every eval run re-invokes the model.

## Decision

Ollama runs natively on the host. All services reach it through a single
`OLLAMA_BASE_URL`, defaulting to `http://host.docker.internal:11434`, with
`extra_hosts: ["host.docker.internal:host-gateway"]` so the same URL resolves on
Linux.

A containerised Ollama exists under the `gpu` compose profile for Linux+NVIDIA.
It is never started on macOS.

## Consequences

- Breaks "100% containerised" for one component. Documented in the README as a
  platform note rather than hidden.
- Deliberately *not* offered as a macOS fallback: a containerised model that
  silently runs ~10x slower is worse than none, because someone will run it and
  conclude the system is slow.
- `OLLAMA_EMBED_DIM` is part of the seam. Changing the embedding model is a
  migration, not a config change: `make verify` asserts the value against
  `poi_embedding`'s actual column dimension and fails loudly. A silent dimension
  mismatch produces garbage similarity scores, never an error.
- Bootstrap requires one manual host step (`ollama pull`), captured in the README.
