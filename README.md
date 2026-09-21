# doe

<img src="./mascot.png" width="130" align="left" alt="doe mascot" />

doe is a lightweight CLI for applying [design of experiments](https://en.wikipedia.org/wiki/Design_of_experiments) methodology to software engineering.

The project defines a directory layout for describing study designs and storing their results together with all code needed to reproduce them. The UX balances the needs of fast-paced experimentation with a reasonable level of scientific rigor. <br clear="left" />

## Install

```
$ go install github.com/felixge/doe@latest
```

## Getting Started

Studies are directories containing one or more designs. Here is a study of different compression algorithms:

```bash
$ cd ./example/compression
$ ls
design.yaml  run.bash  sample.pb  sample.png  sample.txt  setup.bash
```

The heart is the [design.yaml](./example/compression/design.yaml) file which defines a combination of factors and settings, and their execution:

```yaml
setup: './setup.bash'
factors:
  - file: [sample.pb, sample.png, sample.txt]
    algorithm: [gzip, zstd]
    preset: [min, default, max]
run: './run.bash {algorithm} {preset} {file}'
replicates: 6
```

It uses a [setup.bash](./example/compression/setup.bash) script to install dependencies and to emit a flat JSON object describing the environment:

```bash
$ ./setup.bash
{"os":"Darwin","arch":"arm64"}
```

The [run.bash](./example/compression/run.bash) script is invoked `replicates` times for each design point (unique combination of factors and settings) and emits a flat JSON object on the last line of stdout containing the outputs of the run:

```bash
$ ./run.bash zstd default sample.pb
{"level": 3,"wall_seconds": 0.00039,"cpu_seconds": 0.00036,"peak_rss_bytes": 2588672,"input_size_bytes": 26400,"output_size_bytes": 24243}
```

Run an experiment by passing its design path to `doe run`. Incomplete work can be resumed at any time. If any file in the study has changed since the last experiment, doe will refuse to conduct the experiment unless the `-d` flag is provided.

```bash
$ doe run design.yaml
<progress on stderr>
/home/alice/doe/example/compression/results
```

`doe run` prints the results directory to stdout. doe streams one JSON object per run to the `runs.jsonl` file inside that directory. You can analyze this data any way you like, e.g. using DuckDB's `read_json` function:

```bash
$ duckdb -c 'SELECT algorithm, file, level, preset, round(avg(input_size_bytes/output_size_bytes), 2) as ratio, round(avg(input_size_bytes/cpu_seconds/1024/1024), 2) AS throughput, count(1) FROM read_json('./results/runs.jsonl') GROUP BY ALL ORDER BY ALL;'
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

Below you can find an overview of doe commands.

```
A lightweight CLI for design of experiments studies.

Usage: doe <command> [command options] [arguments]

Commands:
	run			Conduct or resume the execution of a study.
```

### run

```
Run performs one experiment per design. Previous runs are reused, allowing work to be resumed.

Usage: doe run [options] <design>...

Arguments:
  <design>...           Paths to one or more design YAML files

Options:
  -p, --plan            Show the design points and schedule. Do not run them.
  -d, --dirty           Run the study even if it will dirty the results.
  -c, --clean           Remove the results and work directories before running.
  -h, --help            Print help text.

Examples:
  # Run a design
  doe run design.yaml
  # Run a design, even if it will produce dirty results
  doe run -d design.yaml
  # Remove previous results and work before running a design
  doe run -c design.yaml
  # Show the plan for the design
  doe run -p design.yaml
```

A run always follows the steps below:

1. If `-c` or `--clean` is provided, remove the study's `results` and `work` directories so the study starts from scratch.
2. Compute the `files_hash` over all files in the study, excluding the `results` and `work` directories and any `.git` directories.
3. Check if `results/experiments.jsonl` contains a `files_hash` for a different version of the study. If yes, refuse to resume unless the `-d` flag is provided or until the user clears the `results` directory.
4. Execute the designs in the order they were listed.
   1. Invoke the `setup` script and capture the env JSON it emits, if any.
   2. Record the experiment for the design in `results/experiments.jsonl` along with the env JSON that was captured.
   3. Expand the `factors` and `replicates` into a list of all runs that need to be performed.
   4. Reuse `results/runs.jsonl` records that belong to the same design, design point and replicate number.
   5. Invoke the `run` script for next remaining run and capture the JSON output it emits.
   6. Record the design point, env and outputs for the run in `results/runs.jsonl`
   7. Continue with any remaining run.
5. Output the path to the `results` directory on stdout.

#### Inspect Plan

The `--plan` flag prints an ASCII table of the deterministic design-point order. Its first column is `#`, numbered from 1. Below it, a schedule table has one row per replicate, a leading replicate label, and one data column per design point; its cells use the `#` values to show execution order.

## Studies

A study is a directory that contains one or more designs, scripts, and other assets needed to conduct experiments. It usually looks like this:

```
experiment1.yaml
experiment2.yaml
setup.bash
run.bash
```

The study root is the parent directory of its design files. All designs passed to one invocation must belong to the same study root.

### Reserved Keywords

Within a study, `results` and `work` are reserved directory names.

For factors and responses, all field names defined in the `runs.jsonl` section below are considered reserved keywords. 

### Designs

Designs are defined as YAML files and must be placed at the top level of the study. The following options exist:

| field      | description                                                  |
| ---------- | ------------------------------------------------------------ |
| setup      | An optional string holding a Bourne shell command to run once before the start of an experiment. It is re-executed when incomplete work is resumed because resuming the execution of a design produces another experiment. May output a JSON object describing the experiment's environment. |
| factors    | A required list of factor groups. Each group maps the same set of factors to lists of settings and produces their Cartesian product. Settings must be JSON scalars: strings, numbers, booleans, or null. The union of these products forms the design points and must not contain duplicates. |
| run        | A required string holding a Bourne shell command that is invoked `replicates` times at every design point. Must produce a JSON object containing the outputs of the run. See Commands & Scripts for more information. See the `runs.jsonl` description below for reserved field names. |
| replicates | An optional integer defining the number of runs to perform at each design point. Defaults to 1. |

doe places design points in a deterministic order that can be inspected with `--plan`. Replicates are numbered from 1. For replicate `r`, doe orders the design points using row `r-1` of a repeating Williams design derived from that order. A complete schedule has `n` rows for an even number of points and `2n` rows for an odd number, then repeats as needed. This balances execution position and first-order carryover effects.

### Scripts

The inline Bourne shell scripts invoked by `setup` and `run` are always executed using the study root as their working directory.

Typically the inline scripts just shell out to a script file in the study. Those scripts can be written in any language. The setup script can install runtime dependencies or perform compilations as needed.

Factor placeholders in `run` are replaced with shell-escaped settings; the command should not add quotes around them. For both `setup` and `run`, doe treats the last line of stdout as the result and streams earlier output to stderr while the experiment runs. The result must be a flat JSON object whose values are JSON scalars. Empty setup output is treated as `{}`; a run must produce a result.

### Work Directory

Temporary files as well as a expensive setup state that may be reused between runs can be stored in a directory called `work`.

### Results Directory

Results are stored in the `results` directory of the study being executed. It contains a record of all experiments and runs.

```
results
	experiments.jsonl
	runs.jsonl
```

#### runs.jsonl

This file contains Newline-Delimited JSON, with each line holding an object as described below.

| field         | description                                                  |
| ------------- | ------------------------------------------------------------ |
| run_id        | A string holding a unique identifier of the run within the study. |
| experiment_id | A string holding the ID of the experiment the run belongs to. |
| replicate     | An integer holding the replicate number of the run, starting at 1. |
| start         | A string holding the [RFC3339Nano](https://pkg.go.dev/time) timestamp that the run was started. |
| end           | A string holding the [RFC3339Nano](https://pkg.go.dev/time) timestamp that the run was finished. |
| ...           | The remainder of the object contains the inputs and outputs of the run. |

Before executing a run, doe checks for an existing run with the same design point and replicate number whose experiment references the same design path. If found, the run is reused; otherwise, it is executed.

#### experiments.jsonl

This file contains Newline-Delimited JSON, with each line holding an object as described below. Each invocation of a design, including an invocation that resumes incomplete work, is an experiment and produces a new line.

| field         | description                                                  |
| ------------- | ------------------------------------------------------------ |
| experiment_id | A string holding the unique identifier of the experiment within the study. |
| start         | A string holding the [RFC3339Nano](https://pkg.go.dev/time) timestamp that an experiment was started. |
| design        | A string holding the path to the YAML file describing the design, relative to the study directory. |
| factors       | An array of strings listing the factors that are being studied. |
| files         | An object with one key per included file path in the study. The value is the hash of the file contents at the time the experiment began. |
| files_hash    | A string holding the hash over all file paths and content hashes in the `files` object, ordered by path in ascending byte order. |
| env           | An object holding the JSON result of the setup script. If setup produced no result, this is an empty object. |
| env_hash      | A string holding the hash over all values in the `env` object after sorting them by their keys in ascending byte order. |

## Terminology

This project aims to use the following terminology consistently.

| term         | description                                                  |
| ------------ | ------------------------------------------------------------ |
| study        | A directory containing one or more designs. It may also contain scripts and other assets. A results directory may be contained within a study, but is not considered to be part of the study itself. |
| design       | A YAML file describing the planned execution of runs.        |
| factor       | An input variable in the design. E.g. `algorithm` or `preset`. |
| setting      | A value associated with a factor. E.g. `gzip` or `low`.      |
| input        | A factor and setting combination. E.g. `algorithm=gzip` or `preset=low`. |
| design point | A unique combination of factors and settings in the design. E.g. `algorithm=gzip preset=low`. |
| response     | An output variable in the design. E.g. `cpu_seconds` or `peak_rss_bytes`. |
| measurement  | A value associated with a response. E.g. `0.025` or `493894`. |
| output       | A response and measurement combination. E.g. `cpu_seconds=0.025 peak_rss_bytes=493894`. |
| experiment   | A single doe invocation of a design. Resuming the execution of a design produces another experiment. |
| run          | A single execution at a design point and the outputs it produced. |
| work         | The directory within a study that contains temporary outputs as well as reusable setup state. |
| results      | The directory within a study that contains its experiment and run records. |
| dirty        | A study is considered to be dirty if it contains results with different `files_hash` values. The use case is adding additional settings to a design after its first execution. |

## AI Usage

doe is designed for clankers (agents) and humans.

Given that clankers can easily generate orchestration frameworks like doe on demand, you might be wondering why you'd want to use doe instead. The answer is that clanker-generated studies are difficult to review, reproduce, and maintain.

doe solves this problem by defining a simple study and results format. This allows both humans and clankers to quickly understand the methodology of a study, analyze the results, and reproduce the experiments if needed.
