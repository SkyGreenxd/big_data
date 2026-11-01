#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
run_id=${RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}
if ! [[ "$run_id" =~ ^[a-zA-Z0-9_-]+$ ]]; then echo 'invalid RUN_ID' >&2; exit 2; fi
orders=${ORDERS:-10000}
products=${PRODUCTS:-30}
reducers=${REDUCERS:-2}
root="/shop/runs/$run_id"
local_dir="runs/$run_id"
mkdir -p "$local_dir"
docker compose up -d
bash scripts/wait-ready.sh
../../bin/pr2 generate --orders "$orders" --products "$products" --seed 42 --out "$local_dir/data"
../../bin/pr2 upload --src "$local_dir/data" --dst "$root/input"
for algorithm in pairs stripes; do
  start=$(date +%s)
  docker compose exec -T resourcemanager bash /work/scripts/run-job.sh \
    "$algorithm" "$root/input/orders.jsonl" "$root/$algorithm" "$reducers" \
    2>&1 | tee "$local_dir/$algorithm.log"
  elapsed=$(($(date +%s)-start))
  printf '%s\t%s\n' "$algorithm" "$elapsed" >> "$local_dir/elapsed-seconds.tsv"
done
../../bin/pr2 compare --pairs "$root/pairs" --stripes "$root/stripes"
for algorithm in pairs stripes; do
  ../../bin/pr2 recommend --algorithm "$algorithm" --catalog "$root/input/catalog.json" \
    --results "$root/$algorithm" --product 'Кофе' | tee "$local_dir/$algorithm-recommendations.txt"
done
cmp "$local_dir/pairs-recommendations.txt" "$local_dir/stripes-recommendations.txt"
printf 'Results: %s\nLogs and elapsed times: %s\n' "$root" "$local_dir"
