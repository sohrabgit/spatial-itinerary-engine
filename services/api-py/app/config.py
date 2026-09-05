"""Configuration. Every value has a working default so a fresh clone runs."""

import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Settings:
    database_url: str = os.getenv(
        "DATABASE_URL", "postgres://app:app@localhost:5432/itinerary"
    )
    ollama_base_url: str = os.getenv("OLLAMA_BASE_URL", "http://localhost:11434")
    ollama_chat_model: str = os.getenv("OLLAMA_CHAT_MODEL", "llama3.2:3b")
    ollama_embed_model: str = os.getenv("OLLAMA_EMBED_MODEL", "nomic-embed-text")
    ollama_embed_dim: int = int(os.getenv("OLLAMA_EMBED_DIM", "768"))
    osrm_base_url: str = os.getenv("OSRM_BASE_URL", "http://localhost:5001")
    solver_seconds: int = int(os.getenv("SOLVER_SECONDS", "3"))


settings = Settings()

# nomic-embed-text task prefixes. These MUST byte-match the Go ingester's
# constants in internal/poi/embedtext.go -- a mismatch costs several points of
# recall and fails silently. There is a test asserting the exact strings.
PREFIX_DOCUMENT = "search_document: "
PREFIX_QUERY = "search_query: "
