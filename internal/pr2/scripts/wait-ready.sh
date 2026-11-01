#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
# Deadline is bounded; inspect container logs if the cluster does not become ready.
for ((attempt=1;attempt<=60;attempt++)); do
  if docker compose exec -T namenode hdfs dfsadmin -report 2>/dev/null | grep -q 'Live datanodes (1)' &&
     docker compose exec -T namenode hdfs dfsadmin -safemode get 2>/dev/null | grep -q 'Safe mode is OFF' &&
     docker compose exec -T resourcemanager yarn node -list 2>/dev/null | grep -q 'RUNNING'; then
    echo 'HDFS and YARN are ready'
    exit 0
  fi
  sleep 2
done
echo 'Hadoop did not become ready. Run: docker compose logs' >&2
exit 1
