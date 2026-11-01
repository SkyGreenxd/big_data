#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."
compose=(docker compose -f internal/pr1/docker-compose.yaml)
for ((attempt=1;attempt<=60;attempt++)); do
  if "${compose[@]}" exec -T namenode hdfs dfsadmin -report 2>/dev/null | grep -q 'Live datanodes (1)' &&
     "${compose[@]}" exec -T namenode hdfs dfsadmin -safemode get 2>/dev/null | grep -q 'Safe mode is OFF'; then
    echo 'HDFS is ready'
    exit 0
  fi
  sleep 2
done
echo 'HDFS did not become ready. Run: docker compose -f internal/pr1/docker-compose.yaml logs' >&2
exit 1
