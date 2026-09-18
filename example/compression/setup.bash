#!/usr/bin/env bash
set -euo pipefail

# We could have run.bash work with both macOS' and Linux' time command,
# but this is a fun way to show the usage of setup scripts.
if [[ $OSTYPE == darwin* ]] && ! command -v gtime >/dev/null; then
  brew install gnu-time
fi

printf '{"os":"%s","arch":"%s"}\n' "$(uname -s)" "$(uname -m)"
