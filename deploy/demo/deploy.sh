#!/bin/bash
# Run over SSH after uploading the workflow's release manifest and config bundle.
set -euo pipefail
umask 077

release=${1:?Release is required}
bundle=${2:?Bundle directory is required}
root=${DEPLOY_ROOT:-/opt/ozon-shortener-demo}
[[ "$release" =~ ^[0-9a-f]{40}-[0-9]+-[0-9]+$ ]] || { echo 'Invalid release'; exit 1; }
test -f "$root/deploy/demo/.env"
exec 9>"$root/.deploy.lock"
flock -n 9 || { echo 'Another deployment is in progress'; exit 1; }
cd "$bundle"
sha256sum --check --strict SHA256SUMS

# Require space for downloads and a database backup; never prune other projects.
available_kb=$(df -Pk "$root" | awk 'NR == 2 {print $4}')
test "$available_kb" -ge 524288 || { echo 'Less than 512 MiB free; deployment stopped'; exit 1; }
install -d -m 700 "$root/releases" "$root/backups"
target="$root/releases/$release"
test ! -e "$target" || { echo 'Release already exists; rerun the GitHub workflow for a new attempt'; exit 1; }
previous="$root"
if test -f "$root/current-release"; then
  previous=$(cat "$root/current-release")
fi
case "$previous" in
  "$root"|"$root"/releases/*) ;;
  *) echo 'Invalid previous release path'; exit 1 ;;
esac
test -f "$previous/deploy/demo/compose.yml"

install -d -m 700 "$target"
python3 - "$bundle" "$target" "$release" <<'PY'
import re, sys, tarfile
from pathlib import Path
bundle, target = map(Path, sys.argv[1:3])
lines = (bundle / 'release.env').read_text().splitlines()
assert len(lines) == 3 and lines[0] == 'RELEASE=' + sys.argv[3], 'Invalid release manifest'
for line, key, repository in zip(lines[1:], ['API_IMAGE', 'GATEWAY_IMAGE'], ['ozon-url-shortener', 'ozon-url-shortener-gateway']):
    assert re.fullmatch(key + '=fsultan13/' + repository + r'@sha256:[0-9a-f]{64}', line), 'Invalid image reference'
with tarfile.open(bundle / 'config.tar.gz') as archive:
    # Reject links and unexpected paths before extraction. No secrets are shipped.
    for member in archive.getmembers():
        path = Path(member.name)
        allowed = (member.name in ('deploy/demo/compose.yml', 'deploy/demo/images.env', 'scripts/check-demo.py')
                   or member.name == 'db/migrations' or member.name.startswith('db/migrations/'))
        assert not path.is_absolute() and '..' not in path.parts and allowed, 'Invalid archive path'
        assert member.isfile() or member.isdir(), 'Archive links are not allowed'
    archive.extractall(target, filter='data')
(target / 'release.env').write_text('\n'.join(lines) + '\n')
PY

compose() {
  local directory=$1
  shift
  local options=(--env-file "$root/deploy/demo/.env" --env-file "$directory/deploy/demo/images.env")
  if test -f "$directory/release.env"; then
    options+=(--env-file "$directory/release.env")
  fi
  docker compose -p ozon-shortener-demo "${options[@]}" -f "$directory/deploy/demo/compose.yml" "$@"
}

changed=0
finish() {
  local status=$?
  trap - EXIT
  if test "$status" -ne 0 && test "$changed" = 1; then
    echo 'Deployment failed; restoring previous API and gateway. Database migrations are not reversed.' >&2
    if compose "$previous" up --no-build --no-deps -d --wait --wait-timeout 180 api-memory api-postgres gateway; then
      echo 'Previous application restored' >&2
    else
      echo 'Application rollback failed; manual intervention required' >&2
    fi
  fi
  exit "$status"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

compose "$target" config --quiet
compose "$target" pull
database_size=$(compose "$previous" exec -T postgres psql -U shortener -d shortener -Atqc 'SELECT pg_database_size(current_database())')
[[ "$database_size" =~ ^[0-9]+$ ]]
available_kb=$(df -Pk "$root" | awk 'NR == 2 {print $4}')
test "$((available_kb * 1024))" -ge "$((database_size * 2 + 536870912))" || { echo 'Insufficient space for database backup'; exit 1; }
compose "$previous" exec -T postgres pg_dump -U shortener -d shortener -Fc > "$root/backups/$release.dump.partial"
test -s "$root/backups/$release.dump.partial"
mv "$root/backups/$release.dump.partial" "$root/backups/$release.dump"
changed=1
compose "$target" up --no-build -d --wait --wait-timeout 180

# Parse resolved public addresses without printing the config or database password.
compose "$target" config --format json | python3 -c '
import json, subprocess, sys
from pathlib import Path
config = json.load(sys.stdin)
subprocess.run([sys.executable, str(Path(sys.argv[1]) / "scripts/check-demo.py"),
    "--memory", config["services"]["api-memory"]["environment"]["PUBLIC_BASE_URL"],
    "--postgres", config["services"]["api-postgres"]["environment"]["PUBLIC_BASE_URL"]], check=True)
' "$target"

printf '%s\n' "$previous" > "$root/.previous-release.tmp"
mv "$root/.previous-release.tmp" "$root/previous-release"
printf '%s\n' "$target" > "$root/.current-release.tmp"
mv "$root/.current-release.tmp" "$root/current-release"
changed=0
echo "Deployment complete: $release"
