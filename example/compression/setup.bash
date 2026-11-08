#!/usr/bin/env bash
set -euo pipefail

# doe passes the study's algorithm settings as a JSON array, e.g. ["gzip","zstd"].
algorithms=$(jq -r '.[]' <<<"$1")

# Install the tool for each algorithm with Homebrew, so macOS runs use GNU gzip
# instead of the system one. run.bash also needs GNU time, which macOS lacks.
# One brew call is fastest; it skips installed formulae without upgrading them.
formulae=$algorithms
[[ $OSTYPE == darwin* ]] && formulae+=$'\ngnu-time'
# shellcheck disable=SC2086 # Split formulae into separate arguments.
HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_UPGRADE=1 brew install $formulae >&2

# Describe the environment, including each tool's version.
env=$(jq -cn --arg os "$(uname -s)" --arg arch "$(uname -m)" '{os: $os, arch: $arch}')
for algorithm in $algorithms; do
  version=$("$algorithm" --version 2>&1)
  env=$(jq -c --arg key "${algorithm}_version" --arg value "${version%%$'\n'*}" '.[$key] = $value' <<<"$env")
done
echo "$env"
