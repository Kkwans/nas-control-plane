#!/usr/bin/env bash
set -euo pipefail

if (($# != 1)) || [[ ! "$1" =~ ^[0-9]{4}\.[0-9]{1,2}\.[0-9]{1,2}-v[0-9]+$ ]]; then
	echo '用法：set-version.sh YYYY.M.D-vN' >&2
	exit 2
fi

version=$1
export NCP_RELEASE_VERSION=$version
root_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
version_file="$root_dir/VERSION"
compose_file="$root_dir/deploy/console/docker-compose.yml"
package_file="$root_dir/frontend/package.json"

printf '%s\n' "$version" > "$version_file"
perl -0pi -e 's/(^[[:space:]]*"version"[[:space:]]*:[[:space:]]*")[^"]+("[[:space:]]*,?)/$1 . $ENV{NCP_RELEASE_VERSION} . $2/em' "$package_file"
perl -0pi -e 's{(^[[:space:]]*BUILD_VERSION:[[:space:]]*)[^\s]+}{$1 . $ENV{NCP_RELEASE_VERSION}}em; s#(^[[:space:]]*image:[[:space:]]*nas-control-plane:)[^\s]+#$1 . $ENV{NCP_RELEASE_VERSION}#em' "$compose_file"

echo "已更新 NCP 版本：$version"
