#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_DIR="$ROOT_DIR/go_client"
ABI="${1:-arm64-v8a}"
API_LEVEL="${ANDROID_NATIVE_API_LEVEL:-21}"
NDK_DIR="${ANDROID_NDK_HOME:-${ANDROID_NDK_ROOT:-}}"
if ! command -v go >/dev/null 2>&1; then
  if [[ -x "/c/Program Files/Go/bin/go.exe" ]]; then
    export PATH="/c/Program Files/Go/bin:$PATH"
  elif [[ -x "/c/Program Files/Go/bin/go" ]]; then
    export PATH="/c/Program Files/Go/bin:$PATH"
  fi
fi

GO_VERSION="$(go version | awk '{print $3}' | sed 's/^go//')"

needs_checklinkname_flag() {
  local major minor
  major="${GO_VERSION%%.*}"
  minor="${GO_VERSION#*.}"
  minor="${minor%%.*}"

  if [[ "$major" -gt 1 ]]; then
    return 0
  fi
  if [[ "$major" -eq 1 && "$minor" -ge 23 ]]; then
    return 0
  fi
  return 1
}

to_posix_path() {
  local p="$1"
  p="${p//$'\r'/}"
  p="${p//\\\\/\\}"
  p="${p//\\:/:}"
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -u "$p"
  else
    echo "$p"
  fi
}

case "$ABI" in
  arm64-v8a)
    GOARCH="arm64"
    CLANG_PREFIX="aarch64-linux-android"
    ;;
  armeabi-v7a)
    GOARCH="arm"
    CLANG_PREFIX="armv7a-linux-androideabi"
    ;;
  x86_64)
    GOARCH="amd64"
    CLANG_PREFIX="x86_64-linux-android"
    ;;
  *)
    echo "Unsupported ABI: $ABI" >&2
    exit 1
    ;;
esac

if [[ -z "$NDK_DIR" && -f "$ROOT_DIR/local.properties" ]]; then
  PROP_NDK="$(grep -E '^ndk\.dir=' "$ROOT_DIR/local.properties" | head -n1 | cut -d= -f2- || true)"
  if [[ -n "$PROP_NDK" ]]; then
    NDK_DIR="$(to_posix_path "$PROP_NDK")"
  fi
fi

if [[ -z "$NDK_DIR" && -f "$ROOT_DIR/local.properties" ]]; then
  SDK_DIR="$(grep -E '^sdk\.dir=' "$ROOT_DIR/local.properties" | head -n1 | cut -d= -f2- || true)"
  if [[ -n "$SDK_DIR" ]]; then
    POS_SDK="$(to_posix_path "$SDK_DIR")"
    NDK_DIR="$(find "$POS_SDK/ndk" -maxdepth 1 -mindepth 1 -type d 2>/dev/null | sort -V | tail -n1 || true)"
  fi
fi

if [[ -z "$NDK_DIR" && -n "${ANDROID_SDK_ROOT:-}" ]]; then
  POS_SDK="$(to_posix_path "$ANDROID_SDK_ROOT")"
  NDK_DIR="$(find "$POS_SDK/ndk" -maxdepth 1 -mindepth 1 -type d 2>/dev/null | sort -V | tail -n1 || true)"
fi

if [[ -z "$NDK_DIR" || ! -d "$NDK_DIR" ]]; then
  echo "NDK not found. Set ANDROID_NDK_HOME or install NDK via Android Studio." >&2
  exit 1
fi

HOST_TAG=""
for tag in windows-x86_64 linux-x86_64 darwin-x86_64 darwin-arm64; do
  if [[ -d "$NDK_DIR/toolchains/llvm/prebuilt/$tag" ]]; then
    HOST_TAG="$tag"
    break
  fi
done

if [[ -z "$HOST_TAG" ]]; then
  echo "NDK prebuilt LLVM toolchain not found in $NDK_DIR/toolchains/llvm/prebuilt" >&2
  exit 1
fi

CC="$NDK_DIR/toolchains/llvm/prebuilt/$HOST_TAG/bin/${CLANG_PREFIX}${API_LEVEL}-clang"
if [[ -f "${CC}.cmd" ]]; then
  CC="${CC}.cmd"
fi

if [[ ! -x "$CC" && ! -f "$CC" ]]; then
  echo "Compiler not found: $CC" >&2
  exit 1
fi

echo "Refreshing Go checksums"
(
  cd "$GO_DIR"
  go mod tidy -e
)

OUT_DIR="$ROOT_DIR/app/src/main/jniLibs/$ABI"
mkdir -p "$OUT_DIR"

echo "Building $ABI -> $OUT_DIR/libclient.so"
(
  cd "$GO_DIR"
  if needs_checklinkname_flag; then
    GOOS=android GOARCH="$GOARCH" CGO_ENABLED=1 CC="$CC" \
      go build -trimpath -ldflags="-s -w -checklinkname=0" -o "$OUT_DIR/libclient.so" .
  else
    GOOS=android GOARCH="$GOARCH" CGO_ENABLED=1 CC="$CC" \
      go build -trimpath -ldflags="-s -w" -o "$OUT_DIR/libclient.so" .
  fi
)

echo "Done: $OUT_DIR/libclient.so"
