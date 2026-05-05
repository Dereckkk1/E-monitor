import asyncpg


async def fetch_commercial(pool: asyncpg.Pool, commercial_id: str) -> dict:
    raise NotImplementedError("implemented in Task 14")


async def write_hashes(pool: asyncpg.Pool, commercial_id: str, variant_id: int,
                       rate_id: int, hashes: list[tuple[int, int]]) -> None:
    raise NotImplementedError("implemented in Task 14")


async def mark_status(pool: asyncpg.Pool, commercial_id: str, status: str,
                      hash_count: int | None = None) -> None:
    raise NotImplementedError("implemented in Task 14")
