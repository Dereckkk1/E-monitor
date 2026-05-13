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


import json
from unittest.mock import patch
from fingerprint.main import handle_generate


@pytest.mark.asyncio
async def test_handle_generate_routes_material_payload(mock_pool, tmp_path):
    pool, conn = mock_pool
    # 1. fetch_material returns a valid row
    audio_file = tmp_path / "test.mp3"
    audio_file.write_bytes(b"fake")
    conn.fetchrow.return_value = {
        "id": "mat-uuid",
        "master_storage_path": str(audio_file),
        "duration_seconds": 30.0,
    }
    nc = AsyncMock()
    msg = MagicMock()
    msg.data = json.dumps({"material_id": "mat-uuid"}).encode()

    # Patch heavy audio work — we only care that the right path is chosen.
    with patch("fingerprint.main.simulate_variants", return_value={0: b"x"}), \
         patch("fingerprint.main.generate_fingerprint", return_value=[(1, 0)]), \
         patch("fingerprint.main.write_hashes", new_callable=AsyncMock):
        await handle_generate(msg, pool, nc)

    # Confirm UPDATE materials was called (mark_material_status), not commercials
    update_sqls = [c[0][0] for c in conn.execute.call_args_list]
    assert any("UPDATE materials" in s for s in update_sqls)
    assert not any("UPDATE commercials" in s for s in update_sqls)

    # Confirm republished payload uses material_id
    publish_payloads = [c[0][1] for c in nc.publish.call_args_list]
    decoded = [json.loads(p.decode()) for p in publish_payloads]
    assert any("material_id" in d for d in decoded)


@pytest.mark.asyncio
async def test_handle_generate_routes_commercial_payload(mock_pool, tmp_path):
    """Regression: the commercial path must still work after the dispatcher refactor."""
    pool, conn = mock_pool
    audio_file = tmp_path / "test.mp3"
    audio_file.write_bytes(b"fake")
    conn.fetchrow.return_value = {
        "id": "com-uuid",
        "master_storage_path": str(audio_file),
        "duration_seconds": 30.0,
    }
    nc = AsyncMock()
    msg = MagicMock()
    msg.data = json.dumps({"commercial_id": "com-uuid"}).encode()

    with patch("fingerprint.main.simulate_variants", return_value={0: b"x"}), \
         patch("fingerprint.main.generate_fingerprint", return_value=[(1, 0)]), \
         patch("fingerprint.main.write_hashes", new_callable=AsyncMock):
        await handle_generate(msg, pool, nc)

    update_sqls = [c[0][0] for c in conn.execute.call_args_list]
    assert any("UPDATE commercials" in s for s in update_sqls)
    assert not any("UPDATE materials" in s for s in update_sqls)

    publish_payloads = [c[0][1] for c in nc.publish.call_args_list]
    decoded = [json.loads(p.decode()) for p in publish_payloads]
    assert any("commercial_id" in d for d in decoded)
