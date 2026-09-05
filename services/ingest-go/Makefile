
.PHONY: snap
snap: ## Store OSRM-snapped routing points (AFTER osrm-foot is healthy)
	cd services/ingest-go && CGO_ENABLED=1 go build -o bin/ingest ./cmd/ingest && \
	  DATABASE_URL=$${DATABASE_URL:-postgres://app:app@localhost:5432/itinerary} \
	  OSRM_BASE_URL=$${OSRM_BASE_URL:-http://localhost:5001} \
	  ./bin/ingest -snap
