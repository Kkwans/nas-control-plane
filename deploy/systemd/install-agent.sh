#!/bin/sh
set -eu

project_root=${1:-/volume2/Project/nas-control-plane}
agent_binary=${2:-"$project_root/bin/ncp-agent-linux-arm64"}
blesh_version=v0.4.0-devel3
blesh_archive=ble-0.4.0-devel3.tar.xz
blesh_url="https://github.com/akinomyoga/ble.sh/releases/download/$blesh_version/$blesh_archive"

install -d -o root -g root -m 0755 /opt/ncp/bin /opt/ncp/etc /opt/ncp/share/blesh
install -o root -g root -m 0755 "$agent_binary" /opt/ncp/bin/ncp-agent
install -o root -g root -m 0644 "$project_root/deploy/systemd/terminal.bashrc" /opt/ncp/etc/terminal.bashrc
install -o root -g root -m 0644 "$project_root/deploy/systemd/ncp-agent.service" /etc/systemd/system/ncp-agent.service
install -o root -g root -m 0644 "$project_root/deploy/systemd/ncp-stack.service" /etc/systemd/system/ncp-stack.service

if [ ! -r /opt/ncp/share/blesh/ble.sh ]; then
  temporary_directory=$(mktemp -d)
  trap 'rm -rf "$temporary_directory"' EXIT INT TERM
  if command -v curl >/dev/null 2>&1; then
    curl --fail --location --silent --show-error "$blesh_url" --output "$temporary_directory/$blesh_archive"
  else
    wget --quiet "$blesh_url" --output-document="$temporary_directory/$blesh_archive"
  fi
  tar -xJf "$temporary_directory/$blesh_archive" \
    -C /opt/ncp/share/blesh \
    --strip-components=1
  test -r /opt/ncp/share/blesh/ble.sh
  chown -R root:root /opt/ncp/share/blesh
fi

systemctl daemon-reload
systemctl enable ncp-agent.service
systemctl enable ncp-stack.service
systemctl restart ncp-agent.service
systemctl --no-pager --full status ncp-agent.service
