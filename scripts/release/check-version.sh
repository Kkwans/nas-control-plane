#!/usr/bin/env bash
set -euo pipefail

root_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
version_file="$root_dir/VERSION"
compose_file="$root_dir/deploy/console/docker-compose.yml"
package_file="$root_dir/frontend/package.json"
server_binary=''
agent_binary=''
expected_tag=''

usage() {
	cat <<'EOF'
用法：check-version.sh [--tag TAG] [--server-binary PATH] [--agent-binary PATH]
EOF
}

while (($# > 0)); do
	case "$1" in
	--tag)
		[[ $# -ge 2 ]] || { usage >&2; exit 2; }
		expected_tag=$2
		shift 2
		;;
	--server-binary)
		[[ $# -ge 2 ]] || { usage >&2; exit 2; }
		server_binary=$2
		shift 2
		;;
	--agent-binary)
		[[ $# -ge 2 ]] || { usage >&2; exit 2; }
		agent_binary=$2
		shift 2
		;;
	--help|-h)
		usage
		exit 0
		;;
	*)
		usage >&2
		exit 2
		;;
	esac
done

version=$(tr -d '\r\n' < "$version_file")
if [[ ! "$version" =~ ^[0-9]{4}\.[0-9]{1,2}\.[0-9]{1,2}-v[0-9]+$ ]]; then
	echo "版本格式无效：$version" >&2
	exit 1
fi

compose_image=$(sed -nE 's/^[[:space:]]*image:[[:space:]]*nas-control-plane:([^[:space:]]+).*$/\1/p' "$compose_file" | head -n 1)
compose_build_version=$(awk '/^[[:space:]]*BUILD_VERSION:/ {print $2; exit}' "$compose_file")
package_version=$(sed -nE 's/^[[:space:]]*"version"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' "$package_file" | head -n 1)

[[ "$compose_image" == "$version" ]] || { echo "Compose image 版本不一致：$compose_image != $version" >&2; exit 1; }
[[ "$compose_build_version" == "$version" ]] || { echo "Compose BUILD_VERSION 不一致：$compose_build_version != $version" >&2; exit 1; }
[[ "$package_version" == "$version" ]] || { echo "前端 package 版本不一致：$package_version != $version" >&2; exit 1; }
[[ -z "$expected_tag" || "$expected_tag" == "$version" ]] || { echo "Git tag 版本不一致：$expected_tag != $version" >&2; exit 1; }

check_binary() {
	local binary=$1
	local label=$2
	[[ -x "$binary" ]] || { echo "$label 不可执行：$binary" >&2; exit 1; }
	local binary_version
	if binary_version=$("$binary" --version 2>/dev/null); then
		[[ "$binary_version" == "$version" ]] || { echo "$label 版本不一致：$binary_version != $version" >&2; exit 1; }
		return
	fi

	# Release runners are usually x86_64 while the published artifact is ARM64.
	# In that case the binary cannot be executed (exec format error), so verify
	# the exact ldflags-injected version string embedded in the ELF instead of
	# silently skipping the artifact check.
	if LC_ALL=C grep -a -F -m 1 -- "$version" "$binary" >/dev/null 2>&1; then
		echo "$label 无法在当前架构执行，已验证嵌入版本：$version" >&2
		return
	fi
	echo "$label 无法读取版本：$binary" >&2
	exit 1
}

[[ -z "$server_binary" ]] || check_binary "$server_binary" ncp-server
[[ -z "$agent_binary" ]] || check_binary "$agent_binary" ncp-agent

echo "version consistency: $version"
