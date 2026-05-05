import logging

import asyncpg

log = logging.getLogger(__name__)


async def fetch_commercial(pool: asyncpg.Pool, commercial_id: str) -> dict:
    async with pool.acquire() as conn:
        row = await conn.fetchrow(
            """
            SELECT id, master_storage_path, duration_seconds
            FROM commercials
            WHERE id = $1
            """,
            commercial_id,
        )
    if row is None:
        raise LookupError(f"commercial {commercial_id} not found")
    return dict(row)


async def mark_status(pool: asyncpg.Pool, commercial_id: str, status: str,
                      hash_count: int | None = None) -> None:
    async with pool.acquire() as conn:
        if status == "ready":
            await conn.execute(
                """
                UPDATE commercials
                SET fingerprint_status = $2,
                    fingerprint_generated_at = NOW(),
                    fingerprint_hash_count = $3
                WHERE id = $1
                """,
                commercial_id, status, hash_count,
            )
        else:
            await conn.execute(
                """
                UPDATE commercials
                SET fingerprint_status = $2
                WHERE id = $1
                """,
                commercial_id, status,
            )


async def write_hashes(pool: asyncpg.Pool, commercial_id: str, variant_id: int,
                       rate_id: int, hashes: list[tuple[int, int]]) -> None:
    if not hashes:
        return
    seen = set()
    rows = []
    for h, t in hashes:
        key = (variant_id, rate_id, h, t)
        if key in seen:
            continue
        seen.add(key)
        rows.append((commercial_id, variant_id, rate_id, h, t))

    async with pool.acquire() as conn:
        await conn.execute(
            """
            DELETE FROM fingerprint_hashes
            WHERE commercial_id = $1 AND variant_id = $2 AND rate_id = $3
            """,
            commercial_id, variant_id, rate_id,
        )
        await conn.copy_records_to_table(
            "fingerprint_hashes",
            records=rows,
            columns=["commercial_id", "variant_id", "rate_id", "hash_value", "time_frame"],
        )
    log.info("wrote %d hashes for commercial=%s variant=%d", len(rows), commercial_id, variant_id)
