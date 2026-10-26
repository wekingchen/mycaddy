#!/usr/bin/env bash
# 下载当前可用的官方 NaiveProxy 正式版 Linux x64 客户端并校验 Release SHA256。
# 不固定已经可能被上游删除附件的旧 Tag；build 与 Release E2E 共用此脚本。
set -euo pipefail

dest="${1:-/tmp/naive}"
mkdir -p "$dest"
: "${GH_TOKEN:?必须设置 GH_TOKEN 才能查询 GitHub Release}"
: "${GITHUB_ENV:?必须在 GitHub Actions 环境调用}"

release_json="$(gh api repos/klzgrad/naiveproxy/releases/latest)"
tag="$(jq -er '.tag_name' <<<"$release_json")"
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+-[0-9]+$ ]]; then
  echo "❌ 官方 NaiveProxy Latest Tag 格式异常：$tag" >&2
  exit 1
fi
archive_name="naiveproxy-${tag}-linux-x64.tar.xz"

asset="$(jq -ec --arg name "$archive_name" '.assets[] | select(.name == $name)' <<<"$release_json")"
url="$(jq -er '.browser_download_url' <<<"$asset")"
digest="$(jq -er '.digest' <<<"$asset")"
if [[ ! "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo "❌ 官方客户端 Release SHA256 缺失或格式异常" >&2
  exit 1
fi

echo "📌 官方 NaiveProxy 最新正式版：$tag"
echo "📦 客户端附件：$archive_name"
curl -fsSL --retry 3 --retry-all-errors --retry-delay 2 \
  "$url" -o "$dest/$archive_name"
printf '%s  %s\n' "${digest#sha256:}" "$dest/$archive_name" | sha256sum -c -
tar -xJf "$dest/$archive_name" -C "$dest"

naive_bin="$(find "$dest" -type f -name naive -print -quit)"
if [[ -z "$naive_bin" || ! -f "$naive_bin" ]]; then
  echo "❌ 官方 NaiveProxy 压缩包里没有 naive 可执行文件" >&2
  exit 1
fi
chmod +x "$naive_bin"
echo "NAIVE_BIN=$naive_bin" >> "$GITHUB_ENV"
echo "NAIVE_VERSION=$tag" >> "$GITHUB_ENV"
echo "✅ 官方 NaiveProxy SHA256 已校验，测试客户端已准备"
"$naive_bin" --version
