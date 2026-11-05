#!/usr/bin/env bash
# Прогон конфигураций на наборе вопросов: ./run_all.sh <questions> <gold> <out_dir> <config>...
set -e
Q=$1; G=$2; OUT=$3; shift 3
mkdir -p "$OUT"
for cfg in "$@"; do
  name=$(basename "$cfg" .json)
  python predict.py --index index --questions "$Q" --output "$OUT/${name}_answers.jsonl" --trace "$OUT/${name}_trace.jsonl" --config "$cfg"
done
