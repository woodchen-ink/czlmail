#!/usr/bin/env bash
# 把 desktop 的 Go 内核编成 Android 用的 libczlcore.so (JNI, 适配层见 desktop/mobile_jni_android.c),
# 按 ABI 放进 native/app/src/main/jniLibs/<abi>/ (不入库)。
#
# 依赖 Go 与 Android NDK: 按 ANDROID_NDK_HOME > ANDROID_NDK_ROOT > $ANDROID_HOME/ndk/<最新版本> 查找。
# 用法: scripts/build-core.sh [abi...]   默认 arm64-v8a (手机) 与 x86_64 (模拟器、x86 平板)。
# 模拟器里不能靠 ARM 转译跑 arm64 内核: 转译器不认 Go 生成的部分指令 (SIGILL)。
# armeabi-v7a 可以编, 但没有验证过, 不放进发版包。
# SQLite 用 cgo 的 mattn/go-sqlite3 (原因见 desktop/internal/sqlitedb), sqlite_fts5 标签开启全文检索。
# 版本号取 VERSION 环境变量, 否则取最近的 git tag。
set -euo pipefail

here="$(cd "$(dirname "$0")/.." && pwd)"
core="$here/../desktop"
out="$here/native/app/src/main/jniLibs"
min_api=26
version="${VERSION:-$(git -C "$core" describe --tags --abbrev=0 2>/dev/null || echo dev)}"

ndk="${ANDROID_NDK_HOME:-${ANDROID_NDK_ROOT:-}}"
if [ -z "$ndk" ]; then
  sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}"
  [ -n "$sdk" ] && [ -d "$sdk/ndk" ] && ndk="$sdk/ndk/$(ls "$sdk/ndk" | sort -V | tail -n1)"
fi
[ -n "$ndk" ] && [ -d "$ndk" ] || { echo "Android NDK not found (set ANDROID_NDK_HOME)"; exit 1; }

case "$(uname -s)" in
  Linux*) host=linux-x86_64; ext="" ;;
  Darwin*) host=darwin-x86_64; ext="" ;;
  MINGW*|MSYS*|CYGWIN*) host=windows-x86_64; ext=".cmd" ;;
  *) echo "unsupported host $(uname -s)"; exit 1 ;;
esac
bin="$ndk/toolchains/llvm/prebuilt/$host/bin"

# build <abi> <goarch> <clang 前缀> [goarm]
build() {
  local abi=$1 goarch=$2 target=$3 goarm=${4:-}
  echo "==> $abi"
  mkdir -p "$out/$abi"
  # max-page-size=16384: Android 15 起有 16KB 内存页的设备, 4KB 对齐的 .so 在上面加载失败。
  (cd "$core" && \
    CGO_ENABLED=1 GOOS=android GOARCH="$goarch" GOARM="$goarm" \
    CC="$bin/${target}${min_api}-clang${ext}" \
    go build -trimpath -buildmode=c-shared -tags sqlite_fts5 \
      -ldflags "-s -w -X main.version=$version -extldflags=-Wl,-soname,libczlcore.so,-z,max-page-size=16384" \
      -o "$out/$abi/libczlcore.so" .)
  rm -f "$out/$abi/libczlcore.h"
}

abis=("$@")
[ ${#abis[@]} -eq 0 ] && abis=(arm64-v8a x86_64)
for abi in "${abis[@]}"; do
  case "$abi" in
    arm64-v8a) build arm64-v8a arm64 aarch64-linux-android ;;
    armeabi-v7a) build armeabi-v7a arm armv7a-linux-androideabi 7 ;;
    x86_64) build x86_64 amd64 x86_64-linux-android ;;
    *) echo "unknown abi $abi"; exit 1 ;;
  esac
done
