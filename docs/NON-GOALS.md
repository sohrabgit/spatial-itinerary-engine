# Non-goals

Scope is the top risk on this project (see the plan's risk register). This file
exists so that "we could also..." has somewhere to go that is not the codebase.

## Not building, and why

**Multi-tenancy and full auth.** Weeks of work, near-zero signal for this domain.
If a scoped slice is wanted later: JWT validation in Go, subject propagated into
OTel baggage, Postgres RLS on the itinerary table. ~150 lines. Stops there.

**Kubernetes / Helm.** Compose is the honest choice for a single-machine system.
Manifests nobody ever runs are negative signal.

**gRPC between Go and Python.** The cold path is a streaming path; SSE covers it
with no protobuf codegen across three CI jobs. Revisit only if the transport
itself becomes a bottleneck.

**A message broker (Kafka / NATS / RabbitMQ).** Dragonfly Streams already
provides a queue if one is needed. Kafka on an 8GB Docker VM is a self-own.

**A hand-rolled VRP metaheuristic.** OR-Tools is 15+ years of engineering. Writing
a worse 2-opt would take two weeks and demonstrate not knowing when to use a
library. The custom algorithm work goes into the fast `repair()` path instead,
where it is genuinely the right tool.

**LLM-as-judge evaluation.** No locally-runnable model we would trust to judge; a
3B model scoring a 3B model's output is noise. Deliberately skipped, and the
reason is published -- that is stronger than reporting an unsound metric.

**Frontend micro-optimisation** (bundle analysis, Lighthouse 100). Not what this
project is demonstrating.

**Cities other than Paris.** The pipeline is region-parameterised and the full
Ile-de-France path works, but the corpus, eval golden set and demo are Paris.

## Explicitly deferred, not rejected

- Self-hosted PMTiles basemap (M5; the seam is already in place)
- Planetiler-built tiles with a baked POI layer
- Multiple `edge-go` replicas coordinated via Dragonfly Streams
- Public-holiday handling in `opening_hours` (v1 ignores `PH off`, and says so)
