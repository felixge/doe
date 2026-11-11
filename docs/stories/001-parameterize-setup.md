# 001: Parameterize setup

- **Status:** Done <!-- Proposed | In Progress | Done | Rejected -->

## Story

As a study author, I want to be able to parameterize my setup scripts, so that I don't need to perform setup actions in my run scripts.

## Context

Right now the setup script doesn't accept placeholder parameters. This means that it can't respond to different factor settings for different experiments. The workaround is moving some setup logic into the run script, but this distorts run durations and ETA estimates.

## Acceptance Criteria

Example: a study with a `go_version` factor.

- [x] The setup can install the go versions needed for the current experiment.
- [x] The run script doesn't need to install any go versions.
- [x] Setup steps that don't depend on the go version, like installing shared tools, still run only once per experiment.
- [x] Run durations and ETA are not impacted by setup steps.
- [x] Adding a go version doesn't prevent reusing existing runs from the other versions.
- [x] Placeholders work consistently between setup and run scripts.

## Notes
