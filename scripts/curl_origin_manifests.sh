#!/usr/bin/env bash
#
# curl origin manifests

for i in {1..10}; do
  echo "=== $(date +%T) ==="
  curl -s http://10.177.0.10:17080/stream.m3u8 | \
    awk '/#EXT-X-MEDIA-SEQUENCE/ || /\.ts/ {print}'
  sleep 1
done
