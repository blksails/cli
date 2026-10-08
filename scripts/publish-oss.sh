#!/usr/bin/env bash
set -euo pipefail

tag="${1:?usage: publish-oss.sh <vX.Y.Z> [dist-dir]}"
dist_dir="${2:-dist}"
ossutil="${OSSUTIL:?OSSUTIL must point to ossutil v2}"
oss_bucket="${OSS_BUCKET:-blksails-pi-desktop}"
public_base="${OSS_PUBLIC_BASE:-https://blksails-pi-desktop.oss-cn-hangzhou.aliyuncs.com}"

if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]]; then
  echo "invalid release tag: $tag" >&2
  exit 2
fi
if [[ ! -d "$dist_dir" ]]; then
  echo "dist directory not found: $dist_dir" >&2
  exit 2
fi

version="${tag#v}"
oss_prefix="oss://${oss_bucket}/bk/releases/${tag}"
release_url="${public_base%/}/bk/releases/${tag}"
manifest="$(mktemp)"
trap 'rm -f "$manifest"' EXIT

assets=()
while IFS= read -r file; do
  assets+=("$file")
done < <(
  find "$dist_dir" -maxdepth 1 -type f \
    \( -name "bk_${version}_*.tar.gz" -o -name "bk_${version}_*.zip" -o -name "checksums.txt" \) \
    -exec basename {} \; | LC_ALL=C sort
)

if [[ " ${assets[*]} " != *" checksums.txt "* ]] || [[ ${#assets[@]} -lt 2 ]]; then
  echo "release assets or checksums.txt missing from $dist_dir" >&2
  exit 2
fi

jq -n \
  --arg tag "$tag" \
  --arg base "$release_url" \
  '{
    tag_name: $tag,
    name: ("bk " + $tag),
    assets: [$ARGS.positional[] | {name: ., url: ($base + "/" + .)}]
  }' \
  --args "${assets[@]}" >"$manifest"

for asset in "${assets[@]}"; do
  "$ossutil" cp "$dist_dir/$asset" "$oss_prefix/$asset" \
    --force --cache-control "public,max-age=31536000,immutable"
done

"$ossutil" cp "$manifest" "$oss_prefix/release.json" \
  --force --cache-control "public,max-age=31536000,immutable" \
  --content-type "application/json"
"$ossutil" cp "$manifest" "oss://${oss_bucket}/bk/releases/latest.json" \
  --force --cache-control "no-store" \
  --content-type "application/json"

echo "published bk ${tag} to ${release_url}"
