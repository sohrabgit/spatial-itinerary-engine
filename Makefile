# Local-only travel recommendation engine.
# Every target is safe to run repeatedly.

SHELL := /bin/bash
.DEFAULT_GOAL := help

COMPOSE     := docker compose
PG_IMAGE    := travel-postgres:17-3.6
MIN_FREE_GB := 15

PSQL = $(COMPOSE) exec -T db psql -U $${POSTGRES_USER:-app} -d $${POSTGRES_DB:-itinerary}

.PHONY: help
help: ## Show available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
	  | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------- environment

.PHONY: check-disk
check-disk: ## Fail if free disk is below MIN_FREE_GB (guards against mid-build corruption)
	@free=$$(df -g / | awk 'NR==2{print $$4}'); \
	if [ "$$free" -lt "$(MIN_FREE_GB)" ]; then \
	  echo "FAIL: $${free}GB free, need $(MIN_FREE_GB)GB."; \
	  echo "      Try: make clean-cache   (safe, reclaims BuildKit cache)"; \
	  echo "      Note: Docker.raw never shrinks on macOS -- also use"; \
	  echo "            Docker Desktop > Resources > Clean / Purge data."; \
	  exit 1; \
	fi; \
	echo "OK: $${free}GB free (need $(MIN_FREE_GB)GB)"

.PHONY: check-docker-mem
check-docker-mem: ## Warn if the Docker VM is too small for the full stack
	@mem=$$(docker info --format '{{.MemTotal}}'); gb=$$((mem / 1073741824)); \
	if [ "$$gb" -lt 15 ]; then \
	  echo "WARN: Docker VM has $${gb}GB. The full stack (Postgres + Dragonfly +"; \
	  echo "      OSRM + services) needs 16-20GB. Raise it in"; \
	  echo "      Docker Desktop > Settings > Resources > Memory."; \
	else echo "OK: Docker VM has $${gb}GB"; fi

# ---------------------------------------------------------------- stack

.PHONY: build
build: ## Build local images
	$(COMPOSE) build

.PHONY: up
up: check-disk check-docker-mem ## Start the serving stack and wait for health
	$(COMPOSE) up -d
	@$(MAKE) --no-print-directory wait-healthy

.PHONY: down
down: ## Stop the stack (keeps volumes)
	$(COMPOSE) down

.PHONY: wait-healthy
wait-healthy:
	@echo "waiting for health..."; \
	for i in $$(seq 1 60); do \
	  unhealthy=$$($(COMPOSE) ps --format '{{.Service}}:{{.Health}}' | grep -v ':healthy$$' | grep -v ':$$' || true); \
	  [ -z "$$unhealthy" ] && break; sleep 2; \
	done; \
	$(COMPOSE) ps --format 'table {{.Service}}\t{{.Status}}'

.PHONY: obs
obs: ## Start the observability stack (Grafana on :3001)
	$(COMPOSE) --profile obs up -d otel-lgtm
	@echo "Grafana: http://localhost:3001"

.PHONY: logs
logs: ## Tail logs
	$(COMPOSE) logs -f --tail=100

.PHONY: psql
psql: ## Open a psql shell
	$(COMPOSE) exec db psql -U $${POSTGRES_USER:-app} -d $${POSTGRES_DB:-itinerary}

# ---------------------------------------------------------------- verify

.PHONY: verify
verify: ## Assert the environment is actually correct (arch, extensions, dims, services)
	@bash scripts/verify.sh

# ---------------------------------------------------------------- hygiene

.PHONY: clean-cache
clean-cache: ## Reclaim BuildKit cache (safe; costs one slow rebuild)
	docker builder prune -af

.PHONY: clean-docker
clean-docker: ## Remove this project's containers, volumes and dangling images
	$(COMPOSE) down -v --remove-orphans
	docker image prune -f

.PHONY: nuke
nuke: ## DESTRUCTIVE: drop the Postgres volume, forcing a full re-ingest
	@read -p "Drop pgdata and force a full re-ingest? [y/N] " ok; [ "$$ok" = "y" ]
	$(COMPOSE) down -v

# ---------------------------------------------------------------- data

.PHONY: migrate
migrate: ## Apply pending schema migrations
	@bash scripts/migrate.sh

.PHONY: post-load
post-load: ## Build HNSW indexes + CLUSTER (run AFTER a bulk ingest)
	$(COMPOSE) exec -T db psql -v ON_ERROR_STOP=1 -U $${POSTGRES_USER:-app} -d $${POSTGRES_DB:-itinerary} < db/post_load.sql

.PHONY: ingest-report
ingest-report: ## Scan the PBF and print POI counts + tag coverage
	cd services/ingest-go && CGO_ENABLED=1 go build -o bin/ingest ./cmd/ingest && \
	  OSM_BBOX=$${OSM_BBOX:-2.20,48.79,2.47,48.92} \
	  ./bin/ingest -pbf ../../data/paris.osm.pbf -filter poi-filter.yaml -report

.PHONY: ingest-context
ingest-context: ## Extract road/transit geometry (Tier B inputs)
	cd services/ingest-go && CGO_ENABLED=1 go build -o bin/ingest ./cmd/ingest && \
	  DATABASE_URL=$${DATABASE_URL:-postgres://app:app@localhost:5432/itinerary} \
	  ./bin/ingest -pbf ../../data/paris.osm.pbf -context

.PHONY: tier-b
tier-b: ## Compute quietness/touristiness/greenness/transit_friction in PostGIS
	$(COMPOSE) exec -T db psql -v ON_ERROR_STOP=1 -U $${POSTGRES_USER:-app} -d $${POSTGRES_DB:-itinerary} < db/tier_b.sql

.PHONY: reembed
reembed: ## Rebuild embed_text with Tier B features and re-embed (AFTER tier-b)
	cd services/ingest-go && CGO_ENABLED=1 go build -o bin/ingest ./cmd/ingest && \
	  DATABASE_URL=$${DATABASE_URL:-postgres://app:app@localhost:5432/itinerary} \
	  ./bin/ingest -reembed

.PHONY: pipeline
pipeline: ## Full data pipeline, in the only order that works
	@echo "1/5 POIs";        $(MAKE) --no-print-directory ingest
	@echo "2/5 context";     $(MAKE) --no-print-directory ingest-context
	@echo "3/5 tier B";      $(MAKE) --no-print-directory tier-b
	@echo "4/5 re-embed";    $(MAKE) --no-print-directory reembed
	@echo "5/5 indexes";     $(MAKE) --no-print-directory post-load

.PHONY: ingest
ingest: ## Scan the PBF, embed POIs, load into Postgres
	cd services/ingest-go && CGO_ENABLED=1 go build -o bin/ingest ./cmd/ingest && \
	  OSM_BBOX=$${OSM_BBOX:-2.20,48.79,2.47,48.92} \
	  DATABASE_URL=$${DATABASE_URL:-postgres://app:app@localhost:5432/itinerary} \
	  ./bin/ingest -pbf ../../data/paris.osm.pbf -filter poi-filter.yaml

.PHONY: popularity
popularity: ## Derive POI notability from OSM cross-references (no external API)
	$(COMPOSE) exec -T db psql -v ON_ERROR_STOP=1 -U $${POSTGRES_USER:-app} -d $${POSTGRES_DB:-itinerary} < db/popularity.sql

# ---------------------------------------------------------------- app

.PHONY: api
api: ## Run the planner API on :8000
	cd services/api-py && \
	  DATABASE_URL=$${DATABASE_URL:-postgres://app:app@localhost:5432/itinerary} \
	  OSRM_BASE_URL=$${OSRM_BASE_URL:-http://localhost:5001} \
	  uv run uvicorn app.main:app --host 127.0.0.1 --port 8000 --reload

.PHONY: web
web: ## Run the web app on :3002 (3000 is often taken)
	cd apps/web && npx next dev --port $${WEB_PORT:-3002}

.PHONY: test-py
test-py: ## Python unit tests
	cd services/api-py && uv run pytest -q

.PHONY: test-go
test-go: ## Go unit tests
	cd services/ingest-go && CGO_ENABLED=1 go test ./...

.PHONY: test
test: test-go test-py ## All unit tests

.PHONY: lint
lint: ## Lint every language
	cd services/api-py && uv run ruff check .
	cd services/ingest-go && go vet ./...
	cd apps/web && npx tsc --noEmit
