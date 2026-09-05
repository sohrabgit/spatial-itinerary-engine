# Custom Postgres image: PostGIS + pgvector, arm64-native.
#
# Why not postgis/postgis? It publishes linux/amd64 only, so on Apple Silicon it
# runs under Rosetta -- 2-5x slower ingest and index builds for no benefit.
# The official postgres image already has the PGDG apt repo configured, and PGDG
# publishes arm64 for bookworm.
# See docs/adr/0001-arm64-native-postgis-pgvector-image.md
FROM postgres:17-bookworm

# Pinned from a verified build (2026-09-05, linux/arm64). Bump deliberately.
ARG POSTGIS_VERSION=3.6.4+dfsg-2.pgdg12+1
ARG PGVECTOR_VERSION=0.8.6-1.pgdg12+1

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      "postgresql-17-postgis-3=${POSTGIS_VERSION}" \
      "postgresql-17-postgis-3-scripts=${POSTGIS_VERSION}" \
      "postgresql-17-pgvector=${PGVECTOR_VERSION}"; \
    rm -rf /var/lib/apt/lists/*

COPY init/ /docker-entrypoint-initdb.d/
