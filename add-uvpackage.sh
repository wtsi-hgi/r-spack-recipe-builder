#!/bin/bash

set -euo pipefail

REPO_DIR="/home/ubuntu/r-spack-recipe-builder"
LOG_DIR="$REPO_DIR/logs"
PKG_LIST_FILE="$REPO_DIR/pypi-pythonpackages.txt"

mkdir -p "$LOG_DIR"

count=0

# Load initial list into an array for batch processing and dynamic renewal
mapfile -t packages < <(sed 's/#.*$//' "$PKG_LIST_FILE" | xargs -r -L1 echo | sed '/^$/d')
idx=0

while [ ${#packages[@]} -gt 0 ]; do
  # Process up to 5 packages
  batch_processed=0
  while [ $batch_processed -lt 5 ] && [ $idx -lt ${#packages[@]} ]; do
    pkg="${packages[$idx]}"
    idx=$((idx + 1))
    if [ -z "$pkg" ]; then
      continue
    fi
  TIMESTAMP=$(date +%s)
  LOG_FILE="$LOG_DIR/uvpackage-$TIMESTAMP-$pkg.log"
  echo "[Package] $pkg @ $(date -Is)" | tee -a "$LOG_FILE" >/dev/null

  set +e
  (
    cd "$REPO_DIR"
    codex exec --sandbox danger-full-access "create a uv package for the pypi entry: $pkg"
  ) 2>&1 | tee -a "$LOG_FILE"
  pkg_status=${PIPESTATUS[0]}
  set -e
  if [ "$pkg_status" -ne 0 ]; then
    echo "[Failure] $pkg exited with status $pkg_status" | tee -a "$LOG_FILE" >/dev/null
  else
    echo "[Success] $pkg completed" | tee -a "$LOG_FILE" >/dev/null
  fi
    batch_processed=$((batch_processed + 1))
    count=$((count + 1))
  done

  # Trigger refine after each batch
  REFINE_TS=$(date +%s)
  REFINE_LOG="$LOG_DIR/refine-$REFINE_TS.log"
  REFINE_OUT="$LOG_DIR/refine-$REFINE_TS.out"
  echo "[Refine] Triggering refine-uvpackages after $count packages @ $(date -Is)" | tee -a "$REFINE_LOG" >/dev/null
  (
    cd "$REPO_DIR"
    bash "$REPO_DIR/refine-uvpackages.sh" "$PKG_LIST_FILE" "$REPO_DIR/pypi-pythonpackages.refined.txt"
  ) > "$REFINE_OUT" 2>&1
  tee -a "$REFINE_LOG" < "$REFINE_OUT" >/dev/null

  new_list=$(grep '^OUTPUT_LIST:' "$REFINE_OUT" | awk -F: '{print $2}' | xargs || true)
  if [ -n "$new_list" ] && [ -s "$new_list" ]; then
    mapfile -t packages < <(sed 's/#.*$//' "$new_list" | xargs -r -L1 echo | sed '/^$/d')
    PKG_LIST_FILE="$new_list"
    idx=0
  else
    # No more packages to process
    break
  fi
done

echo "Run complete. Log: $LOG_FILE"