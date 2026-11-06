#!/usr/bin/env bash
# Прогон конфигураций на наборе вопросов: scripts/run_all.sh <questions> <out_dir> <config>...
set -e
ROOT=$(cd "$(dirname "$0")/.." && pwd)
Q=$1; OUT=$2; shift 2
mkdir -p "$OUT"
for cfg in "$@"; do
  name=$(basename "$cfg" .json)
  python "$ROOT/predict.py" --index "$ROOT/index" --questions "$Q" --output "$OUT/${name}_answers.jsonl" --trace "$OUT/${name}_trace.jsonl" --config "$cfg"
done
