#!/bin/bash

# Runs py-package-uv-creator for all packages (in parallel), then spack installs them (in parallel),
# and writes a list of packages that failed in either phase.

set -u -o pipefail

REPO_DIR="/home/ubuntu/r-spack-recipe-builder"
LOG_DIR="$REPO_DIR/logs"
INPUT_LIST="${1:-$REPO_DIR/pypi-pythonpackages.txt}"
OUTPUT_LIST="${2:-$REPO_DIR/pypi-pythonpackages.refined.txt}"
JOBS="${JOBS:-$(nproc)}"

mkdir -p "$LOG_DIR"

TS=$(date +%s)
FAIL_GEN="$LOG_DIR/failed-gen-$TS.txt"
FAIL_INSTALL="$LOG_DIR/failed-install-$TS.txt"
FAIL_ALL="$LOG_DIR/failed-pypi-$TS.txt"

# Clean previous temp files if they exist for this TS
: > "$FAIL_GEN"
: > "$FAIL_INSTALL"
: > "$FAIL_ALL"

echo "[Info] Using input list: $INPUT_LIST"
echo "[Info] Output renewed list: $OUTPUT_LIST"
echo "[Info] Parallel jobs: $JOBS"

# Optionally ensure the tool is built
if [ ! -x "$REPO_DIR/py-package-uv-creator" ]; then
  echo "[Build] Building py-package-uv-creator..."
  ( cd "$REPO_DIR" && make build | tee -a "$LOG_DIR/build-$TS.log" ) || true
fi

# Prepare the package stream (strip comments/whitespace, remove empty lines)
pkg_stream_cmd="sed 's/#.*$//' \"$INPUT_LIST\" | xargs -r -L1 echo | sed '/^$/d'"

echo "[Phase 1] Generating uv packages in parallel..."
eval "$pkg_stream_cmd" | xargs -I{} -P "$JOBS" bash -lc 'cd '"$REPO_DIR"' && ./py-package-uv-creator -f "{}" > /dev/null 2>&1 || { echo "{}" >> '"$FAIL_GEN"'; exit 0; }'

echo "[Phase 2] Installing with spack in parallel..."
eval "$pkg_stream_cmd" | xargs -I{} -P "$JOBS" bash -lc 'cd '"$REPO_DIR"' && name="{}"; spkg=$(echo "$name" | tr "[:upper:]" "[:lower:]" | sed -E "s/[^a-z0-9]+/-/g; s/^-+//; s/-+$//"); spack install -y "py-$spkg" > '"$LOG_DIR"'/install-{}-'"$TS"'.log 2>&1 || { echo "$name" >> '"$FAIL_INSTALL"'; exit 0; }'

# Combine failures
cat "$FAIL_GEN" "$FAIL_INSTALL" | sed '/^$/d' | sort -u > "$FAIL_ALL"

# Write renewed list
cp "$FAIL_ALL" "$OUTPUT_LIST"

echo "[Done] Failed packages list: $FAIL_ALL"
echo "[Done] Renewed list written to: $OUTPUT_LIST"
echo "OUTPUT_LIST:$OUTPUT_LIST"
echo "[Done] Generation failures: $FAIL_GEN"
echo "[Done] Install failures: $FAIL_INSTALL"

exit 0


