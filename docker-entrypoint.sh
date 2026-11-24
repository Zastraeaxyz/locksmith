#!/bin/sh
set -eu

# Railway mounts volumes as root. Fix ownership of the data directory before
# dropping privileges so the app can write its SQLite file and key.
if [ "$(id -u)" = "0" ]; then
	mkdir -p "$(dirname "${DATABASE_PATH:-/data/locksmith.db}")"
	chown -R 10001:10001 "$(dirname "${DATABASE_PATH:-/data/locksmith.db}")"
	exec su-exec 10001:10001 /app/locksmith
fi

exec /app/locksmith
