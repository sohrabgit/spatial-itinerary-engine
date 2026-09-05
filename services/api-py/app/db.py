"""Postgres access."""

from __future__ import annotations

import asyncpg

from app.config import settings

_pool: asyncpg.Pool | None = None


async def pool() -> asyncpg.Pool:
    global _pool
    if _pool is None:
        _pool = await asyncpg.create_pool(
            settings.database_url.replace("postgres://", "postgresql://"),
            min_size=2, max_size=10, command_timeout=30,
        )
    return _pool


async def close() -> None:
    global _pool
    if _pool is not None:
        await _pool.close()
        _pool = None


async def assert_embedding_dim() -> None:
    """A dimension mismatch yields garbage similarity, never an error."""
    p = await pool()
    got = await p.fetchval(
        """SELECT atttypmod FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid
           WHERE c.relname = 'poi_embedding' AND a.attname = 'embedding'"""
    )
    if got != settings.ollama_embed_dim:
        raise RuntimeError(
            f"poi_embedding is halfvec({got}) but OLLAMA_EMBED_DIM={settings.ollama_embed_dim}"
        )
