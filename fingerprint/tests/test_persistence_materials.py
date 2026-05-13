"""
Tests for the materials-table variants of persistence helpers. Uses a
mock asyncpg pool with the same shape as the real one so we don't need a
live database for unit tests; integration tests against the real DB
happen via docker-compose in Phase 8.
"""

import pytest
from unittest.mock import AsyncMock, MagicMock

from fingerprint.persistence import (
    fetch_material,
    mark_material_status,
)


@pytest.fixture
def mock_pool():
    """An asyncpg.Pool stand-in with controllable fetchrow/execute."""
    pool = MagicMock()
    conn = AsyncMock()
    # async with pool.acquire() as conn:  pattern
    cm = AsyncMock()
    cm.__aenter__.return_value = conn
    cm.__aexit__.return_value = None
    pool.acquire = MagicMock(return_value=cm)
    return pool, conn


@pytest.mark.asyncio
async def test_fetch_material_returns_dict(mock_pool):
    pool, conn = mock_pool
    conn.fetchrow.return_value = {
        "id": "00000000-0000-0000-0000-000000000001",
        "master_storage_path": "/data/masters/abc.mp3",
        "duration_seconds": 30.0,
    }
    result = await fetch_material(pool, "00000000-0000-0000-0000-000000000001")
    assert result["master_storage_path"] == "/data/masters/abc.mp3"
    # Confirm the query targets the materials table, not commercials
    sql_used = conn.fetchrow.call_args[0][0]
    assert "FROM materials" in sql_used
    assert "FROM commercials" not in sql_used


@pytest.mark.asyncio
async def test_fetch_material_raises_when_missing(mock_pool):
    pool, conn = mock_pool
    conn.fetchrow.return_value = None
    with pytest.raises(LookupError, match="material .* not found"):
        await fetch_material(pool, "00000000-0000-0000-0000-000000000099")


@pytest.mark.asyncio
async def test_mark_material_status_ready_writes_hash_count(mock_pool):
    pool, conn = mock_pool
    await mark_material_status(pool, "uuid-x", "ready", hash_count=42)
    assert conn.execute.called
    sql_used = conn.execute.call_args[0][0]
    assert "UPDATE materials" in sql_used
    assert "fingerprint_hash_count" in sql_used


@pytest.mark.asyncio
async def test_mark_material_status_pending_skips_hash_count(mock_pool):
    pool, conn = mock_pool
    await mark_material_status(pool, "uuid-x", "generating")
    sql_used = conn.execute.call_args[0][0]
    assert "UPDATE materials" in sql_used
    assert "fingerprint_hash_count" not in sql_used
