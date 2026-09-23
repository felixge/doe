# doe

<img src="./mascot.png" width="130" align="left" alt="doe mascot" />

doe is a lightweight CLI for applying [design of experiments](https://en.wikipedia.org/wiki/Design_of_experiments) methodology to software engineering.

The tool defines a directory layout for study protocols, scripts, and results. The UX balances the needs of fast-paced experimentation with a reasonable level of scientific rigor. <br clear="left" />

## Install

```bash
$ go install github.com/felixge/doe@latest
```

## Getting Started

Projects are directories containing one or more studies. Here is a study of different compression algorithms:

```bash
$ cd ./example/compression
$ ls
compression.study.yaml  run.bash  sample.pb  sample.png  sample.txt  setup.bash
```

The heart is the [compression.study.yaml](./example/compression/compression.study.yaml) file which defines a shared protocol and named designs with combinations of factors and settings:

```yaml
setup: ./setup.bash
run: ./run.bash {algorithm} {preset} {file}

designs:
  smoke:
    factors:
      file: [sample.txt]
      algorithm: [gzip, zstd]
      preset: [default]

  full:
    factors:
      file: [sample.pb, sample.png, sample.txt]
      algorithm: [gzip, zstd]
      preset: [min, default, max]
    replicates: 6
```

It uses a [setup.bash](./example/compression/setup.bash) script to install dependencies and to emit a JSON object describing the environment:

```bash
$ ./setup.bash
{"os":"Darwin","arch":"arm64"}
```

The [run.bash](./example/compression/run.bash) script is invoked `replicates` times for each design point (unique combination of factors and settings) and emits a JSON object on the last line of stdout containing the outputs of the run:

```bash
$ ./run.bash zstd default sample.pb
{"level": 3,"wall_seconds": 0.00039,"cpu_seconds": 0.00036,"peak_rss_bytes": 2588672,"input_size_bytes": 26400,"output_size_bytes": 24243}
```

Run an experiment by passing the project directory and a design name to `doe run`. Incomplete work can be resumed at any time. If any input to the study has changed since the last experiment, doe will refuse to conduct the experiment unless the `-d` flag is provided.

```bash
$ doe run . full
<progress on stderr>
```

doe streams one JSON object per run to `results/compression/runs.jsonl`. You can analyze this data any way you like, e.g. using DuckDB's `read_json` function:

```bash
$ duckdb -c "SELECT algorithm, file, level, preset, round(avg(input_size_bytes/output_size_bytes), 2) as ratio, round(avg(input_size_bytes/cpu_seconds/1024/1024), 2) AS throughput, count(1) FROM read_json('./results/compression/runs.jsonl') GROUP BY ALL ORDER BY ALL;"
┌───────────┬────────────┬───────┬─────────┬────────┬────────────┬──────────┐
│ algorithm │    file    │ level │ preset  │ ratio  │ throughput │ count(1) │
│  varchar  │  varchar   │ int64 │ varchar │ double │   double   │  int64   │
├───────────┼────────────┼───────┼─────────┼────────┼────────────┼──────────┤
│ gzip      │ sample.pb  │     1 │ min     │   1.08 │      70.94 │        6 │
│ gzip      │ sample.pb  │     6 │ default │   1.09 │      39.41 │        6 │
│ gzip      │ sample.pb  │     9 │ max     │   1.08 │      23.65 │        6 │
│ gzip      │ sample.png │     1 │ min     │    1.0 │       83.0 │        6 │
│ gzip      │ sample.png │     6 │ default │    1.0 │      46.11 │        6 │
│ gzip      │ sample.png │     9 │ max     │    1.0 │      27.67 │        6 │
│ gzip      │ sample.txt │     1 │ min     │  10.12 │      69.33 │        6 │
│ gzip      │ sample.txt │     6 │ default │  10.76 │      38.52 │        6 │
│ gzip      │ sample.txt │     9 │ max     │  11.04 │      23.11 │        6 │
│ zstd      │ sample.pb  │     1 │ min     │   1.09 │      70.94 │        6 │
│ zstd      │ sample.pb  │     3 │ default │   1.09 │      39.41 │        6 │
│ zstd      │ sample.pb  │    22 │ max     │   1.11 │       8.87 │        6 │
│ zstd      │ sample.png │     1 │ min     │    1.0 │       83.0 │        6 │
│ zstd      │ sample.png │     3 │ default │    1.0 │      46.11 │        6 │
│ zstd      │ sample.png │    22 │ max     │    1.0 │      10.38 │        6 │
│ zstd      │ sample.txt │     1 │ min     │  15.61 │      69.33 │        6 │
│ zstd      │ sample.txt │     3 │ default │  15.64 │      38.52 │        6 │
│ zstd      │ sample.txt │    22 │ max     │  16.73 │       8.67 │        6 │
└───────────┴────────────┴───────┴─────────┴────────┴────────────┴──────────┘
```

The rest of this document contains more details on the CLI and doe format.

## CLI

```text
Usage: doe run [options] <project-directory> <design>...

Options:
  -p, --plan    Show design points, schedules, and reusable runs; do not execute.
  -d, --dirty   Allow reuse despite changed study or shared project inputs.
  -h, --help    Print help.
```

doe discovers all top-level `*.study.yaml` files in the project directory. Select designs by qualified `study/design` names or unique short names:

```bash
doe run . compression/smoke compression/full
doe run . smoke full
doe run . compression/smoke latency/full compression/full
```

Short names must resolve uniquely across all discovered studies. Missing, unknown, or ambiguous selectors report qualified choices. Duplicate selections are errors, including short and qualified aliases of the same design.

Before any setup or result writes, doe parses **all** discovered studies, resolves **all** selectors, and captures/checks **each selected study's** snapshot and existing results. Changed inputs require `--dirty`, including with `--plan`, or removal of that study's results by the user.

Designs execute in the exact selector argument order. Setup runs lazily before a study's first selected design and only once for that study in the invocation. Thus `a/x b/y a/z` executes setup A, design x, setup B, design y, design z. Another study's setup may change shared state between designs: projects must make their setups coexist or group their selectors accordingly.

A setup or run failure stops execution, cancels active runs, and retains completed results and command logs. Resuming creates another experiment and reruns setup, even when all requested runs are reusable. Do not run concurrent doe invocations against the same results; there is no process locking.

### Plans and scheduling

`--plan` prints each selected design's point table and schedule in selector order. The `point` column labels points `#1`, `#2`, etc. Schedule rows are run positions and columns are replicates: read a column top to bottom, then move right. `*` marks runs reusable from existing results for the same design. Per-design and total counts report scheduled, reusable, and new runs. Plans do not run setup, write records, or repair interrupted records.

For replicate `r`, doe uses row `r-1` of a repeating Williams design. A complete schedule has `n` rows for an even number of points and `2n` rows for an odd number greater than one. This balances execution position and first-order carryover effects. The schedule repeats when more replicates are requested.

With concurrency, the plan reports the per-group limit, group count, and maximum active runs. Each group gets its own schedule when `concurrency_by` is nonempty. Tables show dispatch priority, not execution timing. Each group fills available slots independently, without row or replicate barriers. Designs remain sequential.

## Study format

A study is a top-level `<name>.study.yaml` file containing a shared experimental protocol and one or more named designs. The filename supplies its identity and results namespace. Renaming a study starts a different namespace. Study and design names use lowercase kebab-case: a lowercase letter followed by lowercase letters/digits, optionally separated by single hyphens; `/` is not part of a name.

| Study field | Description |
| --- | --- |
| `setup` | Optional Bourne shell command, run once per experiment before the study's first selected design. May emit a JSON environment object. |
| `run` | Required nonempty Bourne shell command, shared by all designs. Must emit a JSON result object. |
| `designs` | Required nonempty mapping from design names to their settings below. |

| Design field | Description |
| --- | --- |
| `factors` | Required nonempty mapping from factor names to nonempty lists of distinct settings. Their Cartesian product forms the design points. Settings are JSON scalars: strings, numbers, booleans, or null. |
| `replicates` | Positive number of runs per point; defaults to 1. Replicates start at 1. |
| `concurrency` | Positive maximum active runs **per group**; defaults to 1. |
| `concurrency_by` | List of factor names defining independent concurrency groups. Omitted or empty means one group. |

Every design in a study must use the same factor names. Declaration order may differ. Factor and setting declaration order determines the point order for each design. Unknown fields and duplicate YAML keys are errors.

For example, `concurrency: 2` and `concurrency_by: [host]` allow two active runs per host. Three hosts allow six active runs, with no additional global limit. Scripts must avoid shared-file conflicts. Concurrency can distort measurements and invalidate serial carryover balancing.

### Scripts and shared work

Setup and run commands execute via `/bin/sh` with the **project directory** as their working directory. They can invoke scripts written in any language. Factor placeholders such as `{algorithm}` are replaced with shell-escaped settings; do not add quotes around placeholders.

The final stdout line of each run must be a JSON object. Its response values may include nested objects and arrays. If setup's final stdout line is a JSON object, doe saves it as the environment; otherwise the environment is `{}`. Responses must not collide with factor names or reserved run fields.

Command stdout and stderr are logged together in best-effort arrival order, not streamed to the terminal. Logs include the final JSON line and survive success, failure, and interruption.

`work/` is project-wide and **not namespaced by study**. Scripts can deliberately share build products and expensive setup state there. Put generated files in `work/` or `results/`; other project files contribute to snapshots.

### Snapshots and reuse

Each selected study's snapshot includes its own study file and every other project file, except:

- the project-level `results/` and `work/` trees;
- `.git` entries;
- other `*.study.yaml` files.

Ignored Git files are still included. Included inputs must be regular files, not symlinks. Snapshots store content hashes, not copies of files. Keep the project sources in version control to reproduce an experiment.

Completed runs are reused by **study + design name + design point + replicate**. Full never reuses smoke runs, even for identical points and concurrency settings. Within the same design, reordered factors do not change point identity. Reused records retain their original experiment IDs, environment, timestamps, and logs; no duplicate records are written.

Changes to a design, including its concurrency settings, require `--dirty` when results already exist. This flag permits reuse within the same named design despite changed inputs or scheduling settings. Setup environment hashes are not part of the reuse key. Consider these differences when analyzing results; remove a study's results explicitly when a fresh measurement set is needed.

## Results format

```text
project/
  compression.study.yaml
  work/
  results/
    compression/
      experiments.jsonl
      runs.jsonl
      <experiment_id>/
        setup.txt
        <run_id>.txt
```

Each study has independent experiment/run records. Symlinked result paths are rejected. `setup.txt` exists when setup is specified. Failed runs have logs but no run record. An invalid, unterminated JSONL tail left by interruption is ignored during preflight and removed before execution resumes; malformed terminated records are errors.

### experiments.jsonl

One JSON object records one invocation of a selected study, including all of its selected designs and one setup environment. It is appended after setup succeeds, before runs start. A failed setup leaves its log but no experiment record. Resuming produces a new experiment.

| Field | Description |
| --- | --- |
| `experiment_id` | Unique experiment identifier. |
| `start` | RFC3339Nano timestamp before setup starts. |
| `study` | Study name from the filename. |
| `designs` | Selected design names in argument order for this study, including designs not reached if execution fails. |
| `factors` | Factor names, used to distinguish inputs from outputs in run records. |
| `files` | Map of project-relative included file paths to SHA-256 content hashes. |
| `files_hash` | SHA-256 over sorted paths and their hashes, each followed by a NUL byte. |
| `env` | Setup's final JSON object, or `{}`. |
| `env_hash` | SHA-256 of the JSON environment with object keys sorted. |

### runs.jsonl

One JSON object per successfully completed run, appended in completion order:

| Field | Description |
| --- | --- |
| `run_id` | Unique run identifier. |
| `experiment_id` | Original experiment identifier, joining the run to its protocol snapshot and environment. |
| `design` | Design name within the study. Runs are reused only within this design. |
| `replicate` | Replicate number, starting at 1. |
| `start`, `end` | RFC3339Nano timestamps of run execution. |
| Other fields | Flattened factor settings and response measurements. |

`run_id`, `experiment_id`, `design`, `replicate`, `start`, and `end` are reserved names for both factors and responses.

## Terminology

| Term | Meaning |
| --- | --- |
| project | Directory containing studies, shared scripts/assets, work, and results. |
| study | Shared experimental protocol with named designs, defined by one study file. |
| design | Design points, replication, and scheduling under a study's protocol. |
| factor / setting | Input variable / its value, such as `algorithm` / `gzip`. |
| design point | Unique combination of factor settings. |
| response / measurement | Output variable / its value, such as `cpu_seconds` / `0.025`. |
| experiment | One invocation of a study's selected designs with one setup environment. |
| run | One execution at a design point and replicate, with its measurements. |
| dirty | Current study snapshot differs from a recorded experiment's snapshot. |

## AI Usage

doe is designed for clankers (agents) and humans. A simple study and results format makes experimental methodology, provenance, and measurements easier to review, analyze, and reproduce than ad hoc orchestration scripts.
