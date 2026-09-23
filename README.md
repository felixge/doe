# doe

<img src="./mascot.png" width="130" align="left" alt="doe mascot" />

doe is a lightweight CLI for applying [design of experiments](https://en.wikipedia.org/wiki/Design_of_experiments) methodology to software engineering.

The UX balances the needs of fast-paced experimentation with enough scientific rigor to make results easy to analyze and reproduce. <br clear="left" />

## Getting Started

Doe executes experiments defined via YAML. For example, consider the `sum.study.yaml` file below defining a matrix of factors and a script to invoke for each resulting design point:

```yaml
factors:
  foo: [1, 2, 3]
  bar: [4, 5]
run: |
  printf '{"sum":%s}\n' "$((foo + bar))"
```

The experiment can be run like shown below:

```
$ doe experiment -f sum.study.yaml
```

The tool saves the results to a local directory called `results` and they can be displayed like this:

```
$ doe results
{"foo": "1", "bar": "4", "sum": 5}
{"foo": "2", "bar": "4", "sum": 6}
{"foo": "3", "bar": "4", "sum": 7}
{"foo": "1", "bar": "5", "sum": 6}
{"foo": "2", "bar": "5", "sum": 7}
{"foo": "3", "bar": "5", "sum": 8}
```

Alternatively, positional `key=value` arguments define factors, with values interpreted as YAML. The script for each run is supplied with `-r` (short for `--run`):

```sh
$ doe experiment 'foo=[1, 2, 3]' 'bar=[4, 5]' -r 'printf "{\"sum\":%s}\n" "$((foo + bar))"'
```

Files and CLI options can also be combined, with CLI options taking precedence:

```
$ doe experiment -f sum.study.yaml foo=9
{"foo": "9", "bar": "4", "sum": 13}
{"foo": "9", "bar": "5", "sum": 14}
```

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

