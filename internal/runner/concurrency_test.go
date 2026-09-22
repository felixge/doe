package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExecuteConcurrencyLimitsAndResumes(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		t.Run(fmt.Sprint(grouped), func(t *testing.T) {
			root := t.TempDir()
			writeExecutable(t, filepath.Join(root, "concurrency.sh"), `#!/bin/sh
set -eu
group=$1 id=$2 barrier=$3
mkdir -p work/state
slot=
for i in 1 2; do
  if mkdir "work/state/slot-$group-$i" 2>/dev/null; then slot="work/state/slot-$group-$i"; break; fi
done
if [ -z "$slot" ]; then touch "work/state/violation-$group-$id"; exit 1; fi
trap 'rmdir "$slot" 2>/dev/null || true' EXIT
: >"work/state/started-$group-$id"
i=0
while :; do
  set -- work/state/started-*
  if [ -e "$1" ] && [ "$#" -ge "$barrier" ]; then break; fi
  i=$((i+1)); [ "$i" -lt 100 ] || exit 2
  sleep 0.05
done
printf '{"ok":true}\n'
`)
			group, barrier, by := "all", 2, ""
			if grouped {
				group, barrier, by = "{group}", 4, "    concurrency_by: [group]\n"
			}
			writeFile(t, filepath.Join(root, "a.study.yaml"), fmt.Sprintf("run: ./concurrency.sh %s {id} %d\ndesigns:\n  full:\n    factors: {group: [a, b], id: [1, 2, 3]}\n    replicates: 2\n    concurrency: 2\n%s", group, barrier, by))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			opts := Options{Project: root, Designs: []string{"full"}}
			for range 2 {
				if err := Execute(ctx, testEnv(new(bytes.Buffer), new(bytes.Buffer)), opts); err != nil {
					t.Fatal(err)
				}
				if got := lineCount(t, filepath.Join(root, "results", "a", "runs.jsonl")); got != 12 {
					t.Fatalf("runs=%d", got)
				}
			}
			if matches, _ := filepath.Glob(filepath.Join(root, "work", "state", "violation-*")); len(matches) != 0 {
				t.Fatalf("cap exceeded: %v", matches)
			}
		})
	}
}

func TestExecuteConcurrencyFailureCancelsAndPreservesResults(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "failure.sh"), `#!/bin/sh
set -eu
group=$1 id=$2
mkdir -p work/state
case "$group:$id" in
  success:1) printf '{"ok":true}\n' ;;
  fail:1)
    i=0
    while [ ! -s results/a/runs.jsonl ] || [ ! -e work/state/active ]; do
      i=$((i+1)); [ "$i" -lt 100 ] || exit 2; sleep 0.05
    done
    exit 1 ;;
  active:*) touch work/state/active; sleep 30; printf '{"ok":true}\n' ;;
  *) touch "work/state/queued-$group-$id"; printf '{"ok":true}\n' ;;
esac
`)
	writeFile(t, filepath.Join(root, "a.study.yaml"), "run: ./failure.sh {group} {id}\ndesigns:\n  full:\n    factors: {group: [success, fail, active], id: [1, 2]}\n    concurrency_by: [group]\n")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	err := Execute(ctx, testEnv(new(bytes.Buffer), new(bytes.Buffer)), Options{Project: root, Designs: []string{"full"}})
	if err == nil || ctx.Err() != nil {
		t.Fatalf("error=%v context=%v", err, ctx.Err())
	}
	if _, err := os.Stat(filepath.Join(root, "work", "state", "queued-fail-2")); !os.IsNotExist(err) {
		t.Fatal("queued run dispatched after failure")
	}
	if got := lineCount(t, filepath.Join(root, "results", "a", "runs.jsonl")); got < 1 {
		t.Fatal("successful runs lost")
	}
}
