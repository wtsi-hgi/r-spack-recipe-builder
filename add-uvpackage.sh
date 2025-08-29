#!/bin/bash

set -euo pipefail

REPO_DIR="/home/ubuntu/r-spack-recipe-builder"
LOG_DIR="$REPO_DIR/logs"
PKG_LIST_FILE="$REPO_DIR/pypi-pythonpackages.txt"

mkdir -p "$LOG_DIR"
TIMESTAMP=$(date +%s)
LOG_FILE="$LOG_DIR/uvpackage-$TIMESTAMP.log"

while IFS= read -r raw_pkg || [ -n "$raw_pkg" ]; do
  pkg=$(echo "$raw_pkg" | sed 's/#.*$//' | xargs)
  if [ -z "$pkg" ]; then
    continue
  fi
  echo "[Package] $pkg @ $(date -Is)" | tee -a "$LOG_FILE" >/dev/null
  set +e
  (
    cd "$REPO_DIR"
    codex exec --sandbox danger-full-access "create a uv package for the pypi entry: $pkg and at the meantime improve the script cmd/py-package-uv-creator/main.go and its test main_test.go"
  ) 2>&1 | tee -a "$LOG_FILE"
  pkg_status=${PIPESTATUS[0]}
  set -e
  if [ "$pkg_status" -ne 0 ]; then
    echo "[Failure] $pkg exited with status $pkg_status" | tee -a "$LOG_FILE" >/dev/null
  else
    echo "[Success] $pkg completed" | tee -a "$LOG_FILE" >/dev/null
  fi
done < "$PKG_LIST_FILE"

echo "Run complete. Log: $LOG_FILE"