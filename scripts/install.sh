#!/usr/bin/env bash
set -euo pipefail
DRY_RUN=1
for arg in "$@"; do case "$arg" in --dry-run) DRY_RUN=1;; --apply) DRY_RUN=0;; *) echo "Usage: $0 [--dry-run|--apply]" >&2; exit 2;; esac; done
if [[ "$(uname -s)" != Linux ]]; then echo "This installer supports Linux only." >&2; exit 1; fi
if [[ "$(id -u)" -ne 0 ]]; then echo "Run as root after reviewing this script." >&2; exit 1; fi
for p in 80 443; do if ss -ltn "sport = :$p" | tail -n +2 | grep -q .; then echo "Port $p is already in use; migrate the existing proxy manually first." >&2; exit 1; fi; done
if ! command -v docker >/dev/null; then echo "Install Docker Engine using Docker's official Ubuntu repository before continuing." >&2; exit 1; fi
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
echo "Preflight passed. This reviewed installer only stages the binary/service and does not change firewall or DNS."
echo "Source: $ROOT"
echo "Target: /opt/deploy-panel; service user: deploy-panel; systemd unit: deploy-panel.service"
if [[ $DRY_RUN -eq 1 ]]; then echo "Dry run only. Re-run with --apply after reviewing this file and configuring secrets."; exit 0; fi
echo "Apply mode is intentionally disabled until the operator supplies a reviewed production EnvironmentFile and Traefik ACME configuration." >&2
exit 1
