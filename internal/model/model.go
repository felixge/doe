// Package model contains doe's plain domain data structures.
package model

import "time"

// Scalar is a JSON scalar: nil, a bool, a string, or a number.
//
// Design parsing normalizes YAML numbers to int64, uint64, or float64.
type Scalar = any

// Value associates a factor or response name with a scalar value.
type Value struct {
	Name  string
	Value Scalar
}

// Design describes the work planned by one YAML design file.
type Design struct {
	Path        string
	Setup       string
	FactorNames []string
	Points      []Point
	Run         string
	Replicates  int
}

// Point is one unique combination of factor settings. Values are kept in the
// declaration order established by the first factor group in the design.
type Point struct {
	Values []Value
}

// Study is an in-memory aggregate. Its members are stored by value; persisted
// records refer to designs and experiments by path or ID instead.
type Study struct {
	Root    string
	Designs []Design
}

// Experiment is the persisted account of one invocation of a design.
type Experiment struct {
	ID        string            `json:"experiment_id"`
	Start     time.Time         `json:"start"`
	Design    string            `json:"design"`
	Factors   []string          `json:"factors"`
	Files     map[string]string `json:"files"`
	FilesHash string            `json:"files_hash"`
	Env       map[string]Scalar `json:"env"`
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
	Outputs      map[string]Scalar `json:"-"`
}
