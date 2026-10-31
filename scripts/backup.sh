#!/usr/bin/env bash
set -euo pipefail
if [[ $# -ne 2 ]]; then echo "Usage: $0 DATA_DIR BACKUP_DIR" >&2; exit 2; fi
DATA_DIR="$(realpath -e "$1")"
BACKUP_DIR="$(realpath -m "$2")"
mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"
if [[ ! -f "$DATA_DIR/panel.db" ]]; then echo "SQLite database missing: $DATA_DIR/panel.db" >&2; exit 1; fi
if ! command -v sqlite3 >/dev/null; then echo "sqlite3 is required for a consistent WAL-aware backup" >&2; exit 1; fi
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
sqlite3 "$DATA_DIR/panel.db" ".backup '$BACKUP_DIR/panel-$STAMP.db'"
if [[ -d "$DATA_DIR/traefik" ]]; then cp -a "$DATA_DIR/traefik" "$BACKUP_DIR/traefik-$STAMP"; fi
if [[ -d "$DATA_DIR/logs" ]]; then cp -a "$DATA_DIR/logs" "$BACKUP_DIR/logs-$STAMP"; fi
chmod 600 "$BACKUP_DIR"/panel-"$STAMP".db
echo "Panel backup written to $BACKUP_DIR; back up the master key separately and record application volume names."
