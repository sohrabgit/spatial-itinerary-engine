# Architecture decision records

Short records of decisions that were not obvious, including the ones that turned
out to be wrong. For a portfolio project these are disproportionately valuable:
the thing being evaluated is the reasoning, and these are reasoning in durable
form.

| | decision | why it's here |
|---|---|---|
| [0001](0001-arm64-native-postgis-pgvector-image.md) | Build our own arm64 PostGIS + pgvector image | `postgis/postgis` is amd64-only; Rosetta costs 2–5× on index builds |
| [0002](0002-go-python-workload-split.md) | Split Go and Python by workload character | Splitting by feature produces arbitrary seams; includes the precise (not hand-wavy) GIL argument |
| [0003](0003-host-native-ollama.md) | Run Ollama natively, not in Docker | Docker on macOS has no Metal passthrough; a silently 10× slower container is worse than none |
| [0004](0004-vector-query-classes.md) | Route vector queries into three explicit classes | **Supersedes the plan's assumption.** The predicted recall trap did not occur; the real failure is latency |
| [0005](0005-attribute-tiers-and-inferred-hours.md) | Three attribute tiers, and inferring the hours OSM lacks | "Quiet" has no OSM tag; 60% of POIs have no hours |

## Still to write

- **0006** — TOPTW, and why not a hand-rolled metaheuristic
- **0007** — Why the Dragonfly hot tier is *not* always justified (M3)
- **0008** — The `opening_hours` Go ecosystem gap and routing around it (M2)
- **0009** — Map tiles: OSM raster now, PMTiles behind a seam
