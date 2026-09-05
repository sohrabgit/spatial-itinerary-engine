"""Query embedding."""

from __future__ import annotations

import httpx

from app.config import PREFIX_QUERY, settings


async def embed_query(text: str) -> list[float]:
    """Embed a user query.

    The PREFIX_QUERY prefix is mandatory for nomic-embed-text and must match the
    Go ingester's document-side prefix convention. Omitting it costs several
    points of recall and fails silently.
    """
    async with httpx.AsyncClient(timeout=60.0) as client:
        r = await client.post(
            f"{settings.ollama_base_url}/api/embed",
            json={"model": settings.ollama_embed_model, "input": [PREFIX_QUERY + text]},
        )
        r.raise_for_status()
        vecs = r.json()["embeddings"]
    if len(vecs[0]) != settings.ollama_embed_dim:
        raise RuntimeError(
            f"embed model returned {len(vecs[0])} dims, "
            f"expected {settings.ollama_embed_dim}"
        )
    return vecs[0]


def to_pgvector(v: list[float]) -> str:
    """pgvector text input format."""
    return "[" + ",".join(f"{x:.6g}" for x in v) + "]"
