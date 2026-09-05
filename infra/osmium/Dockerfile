# arm64-native osmium-tool. The published osmium images are amd64-only, and
# running the extract under emulation is needlessly slow for a step that
# touches a 322MB file.
FROM debian:bookworm-slim
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends osmium-tool; \
    rm -rf /var/lib/apt/lists/*
WORKDIR /data
ENTRYPOINT ["osmium"]
