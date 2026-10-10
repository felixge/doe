# doe

<img src="./mascot.png" width="130" align="left" alt="doe mascot" />

doe is a lightweight CLI for applying [design of experiments](https://en.wikipedia.org/wiki/Design_of_experiments) methodology to software engineering.

The UX balances the needs of fast-paced experimentation with enough scientific rigor to make results easy to analyze and reproduce. <br clear="left" />

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

The heart is the [compression.study.yaml](./example/compression/compression.study.yaml) file which defines a shared protocol and named presets with combinations of factors and settings:

```yaml
setup: ./setup.bash
run: ./run.bash {algorithm} {effort} {file}
factors:
  file: [sample.txt]
  algorithm: [gzip, zstd]
  effort: [default]

presets:
  smoke: {}
  full:
    factors:
      file: [sample.pb, sample.png, sample.txt]
      effort: [min, default, max]
    replicates: 6
```

The `full` preset inherits the shared protocol and factors, overriding `file` and `effort` while keeping both algorithms.

It uses a [setup.bash](./example/compression/setup.bash) script to install dependencies and to emit a JSON object describing the environment:

```bash
$ ./setup.bash
{"os":"Darwin","arch":"arm64"}
```

The [run.bash](./example/compression/run.bash) script is invoked `replicates` times for each design point (unique combination of factors and settings) and emits a JSON object on the last line of stdout containing the outputs of the run:

```bash
$ ./run.bash zstd default sample.pb
{"level":3,"wall_seconds":0.00039,"cpu_seconds":0.00036,"peak_rss_bytes":2588672,"input_size_bytes":26400,"output_size_bytes":24243}
```

Run an experiment with `doe run`, a study file, and a preset name. It displays live progress on a terminal, or prints the experiment ID when piped or redirected.

```bash
$ doe run -f compression.study.yaml -p full
```

Successful runs with matching setup and run commands, factor settings, and replicate numbers are reused by default. Use `--no-reuse` to execute all runs, or `-c` to remove previous results first. Reuse does not track script file contents or external dependencies.

CLI options override study and preset settings. For example, add `-n 2 'effort=[min, max]'` to change replicates and factor settings, or `--design` to print the resolved YAML without running.

Use `doe status -f compression.study.yaml` to check the latest experiment, including progress and the active setup or run log path.

doe stores each experiment and run as an individual JSON document. Run inputs are nested under `point`, measurements are nested under `outcome`, and only runs with `state = 'done'` completed successfully:

```text
results/
├── results.lock
├── runs.index.jsonl
└── experiments/
    └── <experiment-id>/
        ├── experiment.json
        ├── setup.log
        └── runs/
            ├── <run-id>.json
            └── <run-id>.log
```

Analyze runs from all experiments in `results` with DuckDB's `read_json` function:

```bash
$ duckdb -c "SELECT point.algorithm AS algorithm, point.file AS file, outcome.level AS level, point.effort AS effort, round(avg(outcome.input_size_bytes/outcome.output_size_bytes), 2) AS ratio, round(avg(outcome.input_size_bytes/outcome.cpu_seconds/1024/1024), 2) AS throughput, count(1) FROM read_json('./results/experiments/*/runs/*.json') WHERE state = 'done' GROUP BY ALL ORDER BY ALL;"
┌───────────┬────────────┬───────┬─────────┬────────┬────────────┬──────────┐
│ algorithm │    file    │ level │ effort  │ ratio  │ throughput │ count(1) │
│  varchar  │  varchar   │ int64 │ varchar │ double │   double   │  int64   │
├───────────┼────────────┼───────┼─────────┼────────┼────────────┼──────────┤
│ gzip      │ sample.pb  │     1 │ min     │   1.08 │      68.44 │        6 │
│ gzip      │ sample.pb  │     6 │ default │   1.09 │      44.51 │        6 │
│ gzip      │ sample.pb  │     9 │ max     │   1.08 │      17.55 │        6 │
│ gzip      │ sample.png │     1 │ min     │    1.0 │      68.59 │        6 │
│ gzip      │ sample.png │     6 │ default │    1.0 │      67.16 │        6 │
│ gzip      │ sample.png │     9 │ max     │    1.0 │      67.15 │        6 │
│ gzip      │ sample.txt │     1 │ min     │  10.12 │     381.32 │        6 │
│ gzip      │ sample.txt │     6 │ default │  10.76 │     204.19 │        6 │
│ gzip      │ sample.txt │     9 │ max     │  11.04 │     110.21 │        6 │
│ zstd      │ sample.pb  │     1 │ min     │   1.09 │     237.15 │        6 │
│ zstd      │ sample.pb  │     3 │ default │   1.09 │     175.48 │        6 │
│ zstd      │ sample.pb  │    22 │ max     │   1.11 │      17.15 │        6 │
│ zstd      │ sample.png │     1 │ min     │    1.0 │     596.86 │        6 │
│ zstd      │ sample.png │     3 │ default │    1.0 │     630.49 │        6 │
│ zstd      │ sample.png │    22 │ max     │    1.0 │      24.67 │        6 │
│ zstd      │ sample.txt │     1 │ min     │  15.61 │     385.04 │        6 │
│ zstd      │ sample.txt │     3 │ default │  15.64 │     378.07 │        6 │
│ zstd      │ sample.txt │    22 │ max     │  18.11 │        3.3 │        6 │
└───────────┴────────────┴───────┴─────────┴────────┴────────────┴──────────┘
```

## Terminology

| term         | description                                                  |
| ------------ | ------------------------------------------------------------ |
| experiment   | A single `doe run` invocation.                               |
| factor       | A factor that influences the outcome of the experiment.      |
| setting      | A value for a factor.                                        |
| factors      | A mapping of factor names to settings. Scalars or sequences form a Cartesian product of design points. |
| input        | A factor=setting pair passed to a run.                       |
| design point | A combination of factor=setting pairs passed to a run.       |
| response     | A name of a measured outcome of the experiment.              |
| measurement  | A value for a response.                                      |
| output       | A response=measurement pair.                                 |
| outcome      | The outputs of a run.                                        |
| run          | A design point carried out to produce measurements.          |

