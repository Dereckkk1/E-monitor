# Stream Health — Design Spec

**Date:** 2026-05-07
**Status:** Approved

---

## 1. Context

The current `/monitoring` page shows only `last_health_check` timestamps and `monitoring_status` badges — no historical data, no timeline, no incident tracking. It is operationally useless.

The `stream_health_events` table already exists in the schema but is unpopulated. The worker sends a heartbeat every ~30 seconds but never records transition events (stream up / stream down).

This feature replaces the monitoring page with a "Saúde do Stream" page that gives operators both a bird's-eye status view and minute-level incident drill-down.

---

## 2. Goals

- Show all stations in active campaigns today with a visual 7-day health summary per row.
- Allow drill-down into any station's timeline with zoom from 7-day view down to minute granularity.
- Populate `stream_health_events` from the worker so the data exists to back the UI.
- Keep `/stations` as a pure management page; add only a small health dot indicator there.

---

## 3. Out of Scope

- Audio quality metrics (LUFS, SNR).
- Alerting / push notifications for downtime.
- Exporting health reports.
- Per-campaign health breakdown (only per-station for now).

---

## 4. Layout — List View (replaces `/monitoring`)

**Page:** `/monitoring` (rename sidebar label to "Saúde do Stream")

### Header

- Title: "Saúde do Stream"
- Subtitle: "{N} emissoras em campanha ativa hoje"
- Filters: search by name, band selector (Todas / FM / AM), health filter (Todas / Com falha / Estável)
- Default sort: error stations first, then alphabetical

### Station Row

Each row contains:

| Element | Description |
|---|---|
| Avatar + name + dial | Band, frequency, city/state |
| Mini 7-day bar | Horizontal bar split into 7 blocks (one per day). Green = up, red = has downtime, gray = no data. Hover shows tooltip per day. |
| Uptime % | Calculated over the last 7 days |
| Last incident | Relative timestamp ("há 3h") or "Sem quedas" |
| Status dot | Green pulsing (active), red (error now), gray (paused) |

Clicking any row opens the drawer. No page navigation.

### Pagination & Performance

- 25 stations per page
- The `/health` summary endpoint returns aggregated data — no raw events in the list query

---

## 5. Layout — Drawer (detail view)

Slides in from the right, covering ~60% of screen width. The list remains visible and navigable on the left (~40%). Clicking a different row in the list swaps the drawer content without closing it.

### Drawer Header

- Station avatar, name, dial
- Current status: "Online há 4h 12min" or "Offline — queda detectada há 8 min"
- Period selector: `1d · 7d` (default: 7d)
- Close button (X) or click outside

### Timeline Component

A continuous horizontal bar representing the selected period, colored by state:

- **Green** — stream was online
- **Red** — stream was offline
- **Gray** — no data (station paused or outside campaign)

**Zoom levels (progressive):**

These are resolution tiers, not fixed window sizes. The visible window is whatever the user selects by dragging; the resolution adapts based on how wide the window is:

| Visible window | Block resolution |
|---|---|
| > 2 days | 1 block = 1 hour |
| 6 hours – 2 days | 1 block = 15 minutes |
| < 6 hours | 1 block = 1 minute |

Example: dragging to select a 30-minute outage region automatically renders at 1-minute resolution.

Interaction:
- Click and drag on the timeline to zoom into the selected region.
- "Reset" button returns to the full period view (7d or 1d, whatever is selected).
- Hover tooltip on any block:
  - Online block: `"Online · 19/05 14:00–15:00 (60 min)"`
  - Offline block: `"Offline · 19/05 14:23–14:31 · duração: 8 min"`

### Below the Timeline — Two Panels

**Left — Summary metrics:**
- Uptime % in period
- Total incidents
- Longest outage (duration + when)
- Average outage duration

**Right — Event list:**
- Reverse-chronological list of downtime events in the period
- Each entry: `date/time · duration · [zoom] button` (clicking zoom snaps the timeline to that incident)
- Scrollable if many events

---

## 6. Backend Changes

### 6.1 Worker — `ingestor/worker.go`

Record state transitions to `stream_health_events`:

| Transition | event_type | When |
|---|---|---|
| Stream comes online | `up` | First successful audio chunk after start or reconnect |
| Stream goes offline | `down` | ffmpeg disconnect, read error, or timeout detected |

On a new `up` event: calculate `duration_seconds = now() - previous_down.event_at` and update the previous `down` row retroactively. The worker tracks the last `down` event ID in memory for this update.

**Worker restart recovery:** on startup, the worker queries `stream_health_events` for the most recent `down` event for its station where `duration_seconds IS NULL`. If found and `last_health_check` is stale (older than 2 minutes), it means the worker crashed mid-outage. It records an `up` event immediately and fills in the `duration_seconds` retroactively from that stored `down` row.

`duration_seconds` on `up` events remains `NULL` until the next `down` arrives (it represents how long the stream stayed online, which is only known in retrospect).

The `details JSONB` field stores optional metadata: error message on `down`, reconnect attempt count, etc.

### 6.2 New API Endpoints

**`GET /v1/internal/stations/health?days=7`**

Returns all stations in active campaigns today with aggregated health summary. Used by the list view.

Response per station:
```json
{
  "station_id": "...",
  "name": "Jovem Pan",
  "band": "FM",
  "frequency_mhz": 98.1,
  "city": "São Paulo",
  "state": "SP",
  "logo_url": "...",
  "monitoring_status": "active",
  "uptime_pct": 99.2,
  "incident_count": 2,
  "last_incident_at": "2026-05-06T14:23:00Z",
  "daily_summary": [
    { "date": "2026-05-01", "uptime_pct": 100 },
    ...
  ]
}
```

**`GET /v1/internal/stations/:id/health?days=7`**

Returns raw events for a single station. Used by the drawer timeline.

Response:
```json
{
  "station_id": "...",
  "period_start": "2026-05-01T00:00:00Z",
  "period_end": "2026-05-07T23:59:59Z",
  "events": [
    { "event_type": "up",   "event_at": "...", "duration_seconds": 14400 },
    { "event_type": "down", "event_at": "...", "duration_seconds": 480 },
    ...
  ]
}
```

### 6.3 "Active campaign today" Filter

JOIN: `stations → commercials → campaigns` where `campaigns.status = 'active'` AND `NOW()` is within `[start_date, end_date]`. The supervisor's in-memory worker registry can serve as a warm cache / fallback.

### 6.4 `/stations` page change

Add a small health status dot to each station row (reuse `.stream-signal` component). No other changes to that page.

---

## 7. Data Model Notes

`stream_health_events` schema (already exists):
```sql
stream_health_events (
  id            BIGSERIAL,
  station_id    UUID,
  event_type    TEXT,         -- 'up' | 'down'
  event_at      TIMESTAMPTZ,
  duration_seconds INT,       -- filled retroactively on 'up' events
  details       JSONB,
  PRIMARY KEY (id, event_at)
)
-- Partitioned by RANGE (event_at), monthly partitions
-- Index on (station_id, event_at DESC)
```

No schema migration needed. The table and indexes are production-ready.

---

## 8. Frontend Tech Notes

- Timeline rendering: plain CSS/SVG — no charting library needed. Each block is a `div` with computed width and color based on event data. Zoom state is local React state.
- Drawer: absolute/fixed positioned panel, animated with CSS transform. No library.
- The list mini-bar uses the same block rendering approach, just smaller.
- Skeleton loaders for both the list and the drawer timeline (consistent with existing `.skeleton` pattern).

---

## 9. Implementation Phases

This can ship incrementally:

1. **Phase 1 — Backend only:** wire up event recording in the worker + two new API endpoints. No UI changes. Lets data accumulate before the UI ships.
2. **Phase 2 — Frontend:** new list page + drawer + timeline component. Replaces monitoring page.
3. **Phase 3 — `/stations` dot:** add health dot to station rows.

Phase 1 is the critical path — the UI is useless without data.
