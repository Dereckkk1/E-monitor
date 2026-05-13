import asyncio
import json
import logging
import os
import signal
import sys

import asyncpg
import nats

from fingerprint.broadcast_sim import simulate_variants
from fingerprint.generator import generate_fingerprint
from fingerprint.persistence import (
    fetch_commercial,
    fetch_material,
    write_hashes,
    mark_status,
    mark_material_status,
)

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s %(message)s",
)
log = logging.getLogger("fingerprint")

SUBJECT_GENERATE = "fingerprint.generate"
SUBJECT_INDEX_RELOAD = "index.reload"
# Shared-hash detection (§18.2.2 follow-up). After write_hashes +
# mark_status('ready') we publish here so the api process can flag
# fingerprint_hashes.is_shared on regions that overlap with existing
# commercials. See docs/shared-hash-detection.md.
SUBJECT_SHARED_SCAN = "fingerprint.shared-scan"


async def handle_generate(msg, pool: asyncpg.Pool, nc: nats.NATS):
    try:
        payload = json.loads(msg.data.decode())
    except Exception as e:
        log.error("invalid JSON payload: %s", e)
        return

    # Support both legacy commercial_id and new material_id payloads.
    if "material_id" in payload:
        entity_id = payload["material_id"]
        entity_kind = "material"
        fetcher = fetch_material
        marker = mark_material_status
        reload_key = "material_id"
    elif "commercial_id" in payload:
        entity_id = payload["commercial_id"]
        entity_kind = "commercial"
        fetcher = fetch_commercial
        marker = mark_status
        reload_key = "commercial_id"
    else:
        log.error("payload missing both material_id and commercial_id: %s", payload)
        return

    log.info("processing %s_id=%s", entity_kind, entity_id)

    try:
        entity = await fetcher(pool, entity_id)
    except Exception as e:
        log.error("fetch_%s failed: %s", entity_kind, e)
        return

    master_path = entity["master_storage_path"]
    if not os.path.isfile(master_path):
        log.error("master file not found: %s", master_path)
        await marker(pool, entity_id, "failed")
        return

    await marker(pool, entity_id, "generating")

    try:
        variants = simulate_variants(master_path)
    except Exception as e:
        log.exception("broadcast_sim failed: %s", e)
        await marker(pool, entity_id, "failed")
        return

    total_hashes = 0
    try:
        for variant_id, audio in variants.items():
            hashes = generate_fingerprint(audio)
            await write_hashes(pool, entity_id, variant_id, rate_id=0, hashes=hashes)
            total_hashes += len(hashes)
    except Exception as e:
        log.exception("generate/write failed: %s", e)
        await marker(pool, entity_id, "failed")
        return

    await marker(pool, entity_id, "ready", hash_count=total_hashes)

    # Republish both the index reload and the shared-scan trigger using the
    # SAME key the upstream sent (material_id or commercial_id). Go-side
    # subscribers handle both shapes (see workers/internal/index/loader.go
    # and workers/internal/sharing/subscriber.go).
    reload_payload = json.dumps({reload_key: entity_id}).encode()
    await nc.publish(SUBJECT_INDEX_RELOAD, reload_payload)
    await nc.publish(SUBJECT_SHARED_SCAN, reload_payload)

    log.info("done %s_id=%s hashes=%d", entity_kind, entity_id, total_hashes)


async def main():
    db_url = os.environ["DATABASE_URL"]
    nats_url = os.environ["NATS_URL"]

    pool = await asyncpg.create_pool(db_url, min_size=1, max_size=5)
    nc = await nats.connect(nats_url, reconnect_time_wait=2, max_reconnect_attempts=-1)

    log.info("connected to nats=%s db=ok", nats_url)

    async def cb(msg):
        await handle_generate(msg, pool, nc)

    sub = await nc.subscribe(SUBJECT_GENERATE, cb=cb)
    log.info("subscribed to %s", SUBJECT_GENERATE)

    stop = asyncio.Event()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, stop.set)

    await stop.wait()
    log.info("shutting down...")
    await sub.unsubscribe()
    await nc.drain()
    await pool.close()


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        sys.exit(0)
