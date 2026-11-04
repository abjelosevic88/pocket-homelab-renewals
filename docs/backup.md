# Backup

One SQLite file: `DATA_DIR/pocket-renewals.db` (WAL mode). Take a consistent copy with `pocket-renewals backup` (writes `DATA_DIR/backups/pocket-renewals-<time>.db` with `VACUUM INTO`) or Settings → Backup & data → Download database (admin). Back up that copy, not the live file. `DATA_DIR/core.token*` is the Pocket Core registration.

Restore: stop the app, replace `pocket-renewals.db` (remove `-wal`/`-shm` files), start.

Every person can also export their own data as JSON (`/api/v1/export.json`, re-importable) or the list as CSV.
