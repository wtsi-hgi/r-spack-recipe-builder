#!/usr/bin/env bash

set -Eeuo pipefail

ROOT_DIR="/home/ubuntu/r-spack-recipe-builder"
# Package name (PyPI), default to peppy if not provided
PKG_NAME="${1:-peppy}"
# Timeout (seconds) for spack install to avoid indefinite hangs
SPACK_INSTALL_TIMEOUT_SECS="${SPACK_INSTALL_TIMEOUT_SECS:-1200}"
cd "$ROOT_DIR"

LOG_DIR="$ROOT_DIR/logs"
mkdir -p "$LOG_DIR"

BIN="$ROOT_DIR/py-package-creator"

build_creator() {
  echo "[build] compiling py-package-creator..."
  # Build the Go binary
  if ! go build -o "$BIN" ./cmd/py-package-creator; then
    echo "[build] failed to compile py-package-creator" >&2
    return 1
  fi
}

run_creator() {
  local ts="$1"
  local logfile="$LOG_DIR/py-package-creator_${ts}.log"
  echo "[create] cleaning packages/*"
  mkdir -p "$ROOT_DIR/packages"
  rm -rf "$ROOT_DIR/packages"/*
  echo "[create] running: $BIN -f ${PKG_NAME}"
  set +e
  "$BIN" -f "${PKG_NAME}" >"$logfile" 2>&1
  local ec=$?
  set -e
  echo "$logfile"
  return $ec
}

run_spack_install() {
  local ts="$1"
  local logfile="$LOG_DIR/spack-install_${ts}.log"
  echo "[spack] installing py-${PKG_NAME} with timeout ${SPACK_INSTALL_TIMEOUT_SECS}s..."
  set +e
  timeout -s TERM -k 30s "${SPACK_INSTALL_TIMEOUT_SECS}" spack install "py-${PKG_NAME}" >"$logfile" 2>&1
  local ec=$?
  set -e
  echo "$logfile"
  return $ec
}

notify_and_iterate() {
  local log_file="$1"
  local reason="$2"
  echo "[notify] invoking cursor-agent due to failure ($reason). See log: $log_file"
  # Funnel the log to cursor-agent with the provided prompt
  cursor-agent -p "there is an error with the py-package-creator and the log is here. ${log_file} the error is that the ${reason}. Please fix the cmd/py-package-creator/main.go and main_test.go to debug. No need to compile or run the code, just edit the code." --model "gpt-5" --output-format text || true
}

main() {
  echo "[start] iterative py-package-creator loop"
  build_creator || true

  while true; do
    ts=$(date +%Y%m%d-%H%M%S)

    # Step 1: run creator
    creator_log=$(run_creator "$ts") || {
      notify_and_iterate "$creator_log" "package-creatore fails"
      build_creator || true
      sleep 2
      continue
    }

    # Step 2: spack install
    spack_log=$(run_spack_install "$ts") || {
      notify_and_iterate "$spack_log" "resulting generated spack package recipe fails"
      build_creator || true
      sleep 2
      continue
    }

    echo "[success] spack install succeeded. Logs: $creator_log , $spack_log"
    exit 0
  done
}

main "$@"

