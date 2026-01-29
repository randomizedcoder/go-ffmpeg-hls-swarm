# Justfile for go-ffmpeg-hls-swarm
# Install: https://github.com/casey/just

default:
    @just --list

# Enhanced container (one command)
enhanced-origin:
    #!/usr/bin/env bash
    set -euo pipefail
    nix build .#test-origin-container-enhanced
    docker load < ./result
    docker run --rm -p 8080:17080 \
      --cap-add SYS_ADMIN \
      --tmpfs /tmp --tmpfs /run --tmpfs /run/lock \
      go-ffmpeg-hls-swarm-test-origin-enhanced:latest

# Main binary container
main-container:
    #!/usr/bin/env bash
    set -euo pipefail
    nix build .#go-ffmpeg-hls-swarm-container
    docker load < ./result
    echo "Container loaded: go-ffmpeg-hls-swarm:latest"
