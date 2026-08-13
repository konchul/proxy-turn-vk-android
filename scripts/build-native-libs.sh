#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

read -r -a target_abis <<< "${TARGET_ABIS:-arm64-v8a armeabi-v7a x86_64}"

for abi in "${target_abis[@]}"; do
  "$ROOT_DIR/scripts/build-go-lib.sh" "$abi"
done
