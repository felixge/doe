#!/usr/bin/env bash
set -euo pipefail

algorithm=$1
preset=$2
file=$3

# Batch inputs so short runs are measurable; wall and CPU are normalized below.
iterations=1000
inputs=()

for ((i = 0; i < iterations; i++)); do
  inputs+=("$file")
done

case "$algorithm:$preset" in
gzip:min) level=1 ;;
gzip:default) level=6 ;;
gzip:max) level=9 ;;
zstd:min) level=1 ;;
zstd:default) level=3 ;;
zstd:max) level=22 ;;
*)
  echo "unsupported compression setting: $algorithm $preset" >&2
  exit 1
  ;;
esac

case "$algorithm" in
gzip)
  command=(gzip -c "-$level" "${inputs[@]}")
  ;;
zstd)
  command=(zstd -qc --ultra "-$level" "${inputs[@]}")
  ;;
esac

stats=$(mktemp)
output=$(mktemp)
trap 'rm -f "$stats" "$output"' EXIT

time_command=/usr/bin/time
[[ $OSTYPE == darwin* ]] && time_command=gtime

"$time_command" -f '%e %U %S %M' -o "$stats" "${command[@]}" >"$output"
input_size=$(wc -c <"$file")
output_size=$(($(wc -c <"$output") / iterations))
awk -v iterations="$iterations" -v level="$level" -v input_size="$input_size" -v output_size="$output_size" '
  {
    printf "{\"level\":%.0f,\"wall_seconds\":%g,\"cpu_seconds\":%g,\"peak_rss_bytes\":%.0f,\"input_size_bytes\":%.0f,\"output_size_bytes\":%.0f}\n",
      level, $1 / iterations, ($2 + $3) / iterations, $4 * 1024, input_size, output_size
  }
' "$stats"
