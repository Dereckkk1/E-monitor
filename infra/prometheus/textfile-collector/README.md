# Textfile collector — backup & tiering metrics

The `backup.sh` and `restore-test.sh` shell scripts run outside the Go process,
so they expose their metrics by writing `.prom` files into a directory that
`node_exporter` is configured to scrape via `--collector.textfile.directory`.

## Why textfile, not pushgateway?

- The scripts are short-lived; pushgateway is for long-running batch jobs that
  need their own retention story.
- Restore tests run monthly; we don't want a stale pushgateway entry to mask a
  newer cron failure.
- The textfile collector is already part of the standard `node_exporter`
  deployment in `§14.5` (Provisionamento), so no new component is needed.

## Setup

1. Pick a directory, e.g. `/var/lib/node_exporter/textfile`.
2. Make sure `node_exporter` runs with:
   ```
   --collector.textfile.directory=/var/lib/node_exporter/textfile
   ```
3. Set `PROM_TEXTFILE_DIR=/var/lib/node_exporter/textfile` in the environment
   the cron jobs read.
4. Confirm Prometheus scrapes `node_exporter`. The metrics will appear under
   their original names (no prefix added by the collector):
   - `radiocheck_postgres_backup_last_success_timestamp`
   - `radiocheck_postgres_backup_size_bytes`
   - `radiocheck_postgres_backup_duration_seconds`
   - `radiocheck_postgres_backup_last_status`
   - `radiocheck_postgres_restore_test_last_status`
   - `radiocheck_postgres_restore_test_last_run_timestamp`

## Permissions

The cron user must own (or be allowed to write to) `PROM_TEXTFILE_DIR`. The
scripts write to a `.tmp.<pid>` file then `mv` atomically, so partial reads
are not a concern.

## Tiering metrics

The evidence tiering job runs in the `api` Go process and exposes its metrics
directly through `/metrics` (Prometheus scrapes the API service):

- `radiocheck_evidence_tier_movements_total{from,to}`
- `radiocheck_evidence_storage_bytes{tier}`
- `radiocheck_evidence_tiering_last_run_timestamp`
- `radiocheck_evidence_tiering_errors_total`

No textfile collector entry is needed for those.
