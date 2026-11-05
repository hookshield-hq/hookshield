#!/usr/bin/env sh
# Dumps the HookShield database (custom format, compressed) into ./backups and
# keeps the last HS_BACKUP_KEEP_DAYS days (default 14).
#
#   ./backup.sh                 # → backups/hookshield-20261103T030000Z.dump
#   ./backup.sh /mnt/offsite    # another directory
#
# Cron (every night at 03:00):
#   0 3 * * * cd /opt/hookshield/deploy && mkdir -p backups && ./backup.sh >> backups/backup.log 2>&1
#
# A dump is useless without the encryption key: keep a copy of it somewhere
# else (password manager), once:
#   docker compose exec api cat /var/lib/hookshield/encryption.key
set -eu
cd "$(dirname "$0")"

dir=${1:-./backups}
keep=${HS_BACKUP_KEEP_DAYS:-14}
mkdir -p "$dir"
umask 077

stamp=$(date -u +%Y%m%dT%H%M%SZ)
file="$dir/hookshield-$stamp.dump"
docker compose exec -T postgres pg_dump -U hookshield -d hookshield --format=custom > "$file.partial"
mv "$file.partial" "$file"
echo "✓ $(date -u +%FT%TZ) $file ($(du -h "$file" | cut -f1))"

find "$dir" -name 'hookshield-*.dump' -type f -mtime "+$keep" -print -delete | sed 's/^/  removed /'
