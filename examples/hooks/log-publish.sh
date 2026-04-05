#!/bin/sh
set -eu

printf '%s|%s|%s|%s\n' \
  "$SERIAL_SYNC_TARGET_ID" \
  "$SERIAL_SYNC_TARGET_KIND" \
  "$SERIAL_SYNC_ARTIFACT_ID" \
  "$SERIAL_SYNC_ARTIFACT_PATH"
