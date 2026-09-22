// Package model contains doe's plain domain data structures.
package model

import "time"

// IsReservedRunField reports whether name is owned by the runs.jsonl format.
func IsReservedRunField(name string) bool {
	switch name {
	case "run_id", "experiment_id", "replicate", "start", "end":
		return true
	default:
		return false
	}
}

// Scalar is a JSON scalar: nil, a bool, a string, or a number.
//
// Design parsing normalizes YAML numbers to int64, uint64, or float64.
type Scalar = any

// Value associates a factor name with a scalar value.
type Value struct {
	Name  string
	Value Scalar
}

// Design describes named design points, replication, and scheduling in a study.
type Design struct {
	Name          string
	FactorNames   []string
	Points        []Point
	Replicates    int
	Concurrency   int
	ConcurrencyBy []string
}

// Point is one unique combination of factor settings. Values are kept in the
// factor declaration order in the design.
type Point struct {
	Values []Value
}

// Study defines a shared experimental protocol and its named designs.
type Study struct {
	Name    string
	Path    string
	Setup   string
	Run     string
	Designs []Design
}

// Experiment records one invocation of a study's selected designs.
type Experiment struct {
	ID        string            `json:"experiment_id"`
	Start     time.Time         `json:"start"`
	Study     string            `json:"study"`
	Designs   []string          `json:"designs"`
	Factors   []string          `json:"factors"`
	Files     map[string]string `json:"files"`
	FilesHash string            `json:"files_hash"`
	Env       map[string]any    `json:"env"`
	EnvHash   string            `json:"env_hash"`
}

// Run is one execution at a design point. Inputs and Outputs are represented
// separately in memory; the results writer flattens them in runs.jsonl.
type Run struct {
	ID           string            `json:"run_id"`
	ExperimentID string            `json:"experiment_id"`
	Replicate    int               `json:"replicate"`
	Start        time.Time         `json:"start"`
	End          time.Time         `json:"end"`
	Inputs       map[string]Scalar `json:"-"`
	Outputs      map[string]any    `json:"-"`
}
