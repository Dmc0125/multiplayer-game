#!/usr/bin/env bash

set -euo pipefail

project_dir=$(cd $(dirname "${BASH_SOURCE[0]}") && pwd)

PPROF_URL="${PPROF_URL:-http://127.0.0.1:6060}"
OUT_DIR="${1:-profiles-$(date +%Y%m%d-%H%M%S)}"
OUT_DIR="$project_dir/$OUT_DIR"

mkdir -p "$OUT_DIR"

echo "Started profiling"

curl -fsS \
	-o "$OUT_DIR/heap.pb.gz" \
	"$PPROF_URL/debug/pprof/heap?gc=1"

curl -fsS \
	-o "$OUT_DIR/cpu.pb.gz" \
	"$PPROF_URL/debug/pprof/profile?seconds=30"

curl -fsS \
	-o "$OUT_DIR/goroutine.txt" \
	"$PPROF_URL/debug/pprof/goroutine?debug=2"

echo "Profiles written to $OUT_DIR"

# curl -fsS \
# 	-o "$OUT_DIR/mutex.pb.gz" \
# 	"$PPROF_URL/debug/pprof/mutex"
#
# curl -fsS \
# 	-o "$OUT_DIR/block.pb.gz" \
# 	"$PPROF_URL/debug/pprof/block"
#
# go tool pprof \
# 	-http=127.0.0.1:6061 \
# 	-inuse_space \
# 	"$OUT_DIR/heap.pb.gz"

go tool pprof \
	-http=127.0.0.1:6062 \
	"$OUT_DIR/cpu.pb.gz"

# go tool pprof \
# 	-png \
# 	"$OUT_DIR/mutex.pb.gz" \
# 	> "$OUT_DIR/mutex.svg"
#
# go tool pprof \
# 	-png \
# 	"$OUT_DIR/block.pb.gz" \
# 	> "$OUT_DIR/block.svg"

