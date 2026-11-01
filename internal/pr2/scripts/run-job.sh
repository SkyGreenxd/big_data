#!/usr/bin/env bash
# Runs inside the ResourceManager container.
set -euo pipefail
algorithm=${1:?usage: run-job.sh pairs|stripes input output [reducers]}
input=${2:?missing HDFS input path}
output=${3:?missing HDFS output path}
reducers=${4:-2}
case "$algorithm" in pairs|stripes) ;; *) echo 'algorithm must be pairs or stripes' >&2; exit 2 ;; esac
if ! [[ "$reducers" =~ ^[1-9][0-9]*$ ]]; then
  echo 'reducers must be a positive integer' >&2
  exit 2
fi
# Hadoop fails if output exists; never delete previous results automatically.
exec mapred streaming \
  -D "mapreduce.job.name=shop-$algorithm" \
  -D "mapreduce.job.reduces=$reducers" \
  -D mapreduce.input.fileinputformat.split.maxsize=1048576 \
  -files /work/bin/pr2-linux#pr2 \
  -input "$input" \
  -output "$output" \
  -mapper "pr2 map --algorithm $algorithm" \
  -combiner "pr2 reduce --algorithm $algorithm" \
  -reducer "pr2 reduce --algorithm $algorithm"
