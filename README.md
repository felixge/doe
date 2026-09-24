# doe

<img src="./mascot.png" width="130" align="left" alt="doe mascot" />

doe is a lightweight CLI for applying [design of experiments](https://en.wikipedia.org/wiki/Design_of_experiments) methodology to software engineering.

The UX balances the needs of fast-paced experimentation with enough scientific rigor to make results easy to analyze and reproduce. <br clear="left" />

## Install

```bash
$ go install github.com/felixge/doe2@latest
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
run: ./run.bash {algorithm} {preset} {file}
factors:
  file: [sample.txt]
  algorithm: [gzip, zstd]
  preset: [default]

presets:
  smoke: {}
  full:
    factors:
      file: [sample.pb, sample.png, sample.txt]
      preset: [min, default, max]
    replicates: 6
```

The `full` preset inherits the shared protocol and factors, overriding `file` and `preset` while keeping both algorithms.

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

Run an experiment by passing the study file and a preset name to `doe experiment`. The command prints the experiment ID on success. Use `-c` to remove previous results before running again.

```bash
$ doe experiment -f compression.study.yaml -p full
<experiment ID on stdout>
```

doe saves one JSON object per run to `results/runs.jsonl`. You can analyze this data any way you like, e.g. using DuckDB's `read_json` function:

```bash
$ duckdb -c "SELECT algorithm, file, level, preset, round(avg(input_size_bytes/output_size_bytes), 2) as ratio, round(avg(input_size_bytes/cpu_seconds/1024/1024), 2) AS throughput, count(1) FROM read_json('./results/runs.jsonl') GROUP BY ALL ORDER BY ALL;"
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

Study defaults are inherited by the selected preset; CLI options override both. For example, `doe experiment -f compression.study.yaml -p smoke -n 3 file=sample.pb` runs the smoke preset with three replicates of each design point on `sample.pb`. Use `-s` and `-r` to override the setup and run commands.

## Terminology

| term         | description                                                  |
| ------------ | ------------------------------------------------------------ |
| experiment   | A single `doe experiment` invocation.                        |
| factor       | A factor that influences the outcome of the experiment.      |
| setting      | A value for a factor.                                        |
| matrix       | A mapping of factor:settings where settings can either be a single setting or a sequence of values that is used to form a cartesian product with the other factor=settings pairs in the matrix. |
| input        | A factor=setting pair passed to a run.                       |
| design point | A combination of factor=setting pairs passed to a run.       |
| response     | A name of a measured outcome of the experiment.              |
| measurement  | A value for a response.                                      |
| output       | A response=measurement pair.                                 |
| outcome      | The outputs of a run.                                        |
| run          | A design point carried out to produce measurements.          |

