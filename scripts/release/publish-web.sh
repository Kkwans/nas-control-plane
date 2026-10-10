#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 3 ]]; then
  echo '用法：publish-web.sh <已验证构建的dist> <已部署的dist> <网页地址>' >&2
  exit 2
fi
source_dist=$(realpath "$1")
target_dist=$(realpath "$2")
base_url=$3
repo_root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
[[ "$source_dist" != "$target_dist" && -f "$source_dist/index.html" && -d "$source_dist/assets" && -f "$target_dist/index.html" && -d "$target_dist/assets" ]] || {
  echo '发布必须使用独立且已存在的源码产物与部署目录' >&2
  exit 1
}
python3 /volume2/Common/governance/scripts/check-placement.py --path "$target_dist" --operation mkdir --kind artifact
# Keep old hashed assets for clients that still have the previous HTML cached.
rsync -a --exclude=index.html "$source_dist/" "$target_dist/"
# NAS transfer can change effective file modes. Normalize the destination,
# not just the build directory; switch the HTML only after its assets are readable.
node "$repo_root/frontend/scripts/normalize-dist-permissions.mjs" "$target_dist"
install -m 0644 "$source_dist/index.html" "$target_dist/index.html"
node "$repo_root/frontend/scripts/verify-web-assets.mjs" "$source_dist" "$base_url"
