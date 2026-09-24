// Package model defines doe studies and loads them from YAML.
package model

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"uuid"

	"gopkg.in/yaml.v3"
)

// Study is the YAML protocol for a single experiment.
type Study struct {
	Design `yaml:",inline"`
}

// NewStudy creates a study with one replicate per design point.
func NewStudy() Study {
	return Study{Replicates: 1}
}

// Load decodes a study file into the receiver.
func (s *Study) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(data, s); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// Design defines the factors and scripts for an experiment.
type Design struct {
	Factors    Factors `json:"factors" yaml:"factors"`
	Setup      Script  `json:"setup,omitempty" yaml:"setup"`
	Run        Script  `json:"run" yaml:"run"`
	Replicates int     `json:"replicates" yaml:"replicates"`
}

// Validate checks the final design after applying study and CLI settings.
func (d Design) Validate() error {
	if len(d.Factors) == 0 {
		return fmt.Errorf("at least one factor is required")
	}
	if d.Run == "" {
		return fmt.Errorf("a run script is required")
	}
	if d.Replicates < 1 {
		return fmt.Errorf("replicates must be a positive integer")
	}
	return nil
}

// Clone returns a design with an independent factors map.
func (d Design) Clone() Design {
	d.Factors = maps.Clone(d.Factors)
	return d
}

// Points returns the cartesian product of the design's factor settings.
// Factors are sorted so the resulting order is stable.
func (d Design) Points() []Point {
	factors := make([]Factor, 0, len(d.Factors))
	for factor := range d.Factors {
		factors = append(factors, factor)
	}
	slices.Sort(factors)

	var points []Point
	current := make(Point, len(factors))
	var visit func(int)
	visit = func(index int) {
		if index == len(factors) {
			points = append(points, maps.Clone(current))
			return
		}
		factor := factors[index]
		for _, setting := range d.Factors[factor] {
			current[factor] = setting
			visit(index + 1)
		}
	}
	visit(0)
	return points
}

// Results holds the experiments recorded by doe.
type Results struct {
	Experiments []Experiment `json:"experiments"`
}

// Experiment is a single invocation of a study. Its ID is a UUIDv7.
type Experiment struct {
	ID     uuid.UUID `json:"id"`
	Env    Env       `json:"env"`
	Design `json:"design"`
}

// NewExperiment creates an experiment from a study with a UUIDv7 ID.
func NewExperiment(study Study) Experiment {
	return Experiment{
		ID:     uuid.NewV7(),
		Design: study.Clone(),
	}
}

// Env holds the environment information emitted by an experiment's setup script.
type Env map[string]any

// Factors maps each factor to its possible settings.
type Factors map[Factor]Settings

// Set parses a YAML setting or sequence and replaces a factor's settings.
func (f *Factors) Set(factor Factor, data []byte) error {
	var settings Settings
	if err := yaml.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("factor %q: %w", factor, err)
	}
	if *f == nil {
		*f = make(Factors)
	}
	(*f)[factor] = settings
	return nil
}

// Factor names an input to a run.
type Factor string

// Point maps each factor to its setting for a single run.
type Point map[Factor]Setting

// Run is an execution of a design point, identified by a UUID.
// JSON tags are unnecessary because MarshalJSON handles serialization.
type Run struct {
	ID           uuid.UUID
	ExperimentID uuid.UUID
	Point        Point
	Replicate    int
	Outcome      Outcome
}

// NewRun creates a run for an experiment, design point, and one-based replicate with a UUIDv7 ID.
func NewRun(experimentID uuid.UUID, point Point, replicate int) Run {
	return Run{ID: uuid.NewV7(), ExperimentID: experimentID, Point: point, Replicate: replicate}
}

// MarshalJSON writes the ID, replicate, point settings, and outcome as a flat object.
func (r Run) MarshalJSON() ([]byte, error) {
	fields := map[string]any{"id": r.ID, "experiment_id": r.ExperimentID, "replicate": r.Replicate}
	for factor, setting := range r.Point {
		if _, exists := fields[string(factor)]; exists {
			return nil, fmt.Errorf("run factor %q conflicts with reserved field", factor)
		}
		fields[string(factor)] = setting
	}
	for response, measurement := range r.Outcome {
		if _, exists := fields[string(response)]; exists {
			if _, factor := r.Point[Factor(response)]; factor {
				return nil, fmt.Errorf("run outcome conflicts with factor %q", response)
			}
			return nil, fmt.Errorf("run response %q conflicts with reserved field", response)
		}
		fields[string(response)] = measurement
	}
	return json.Marshal(fields)
}

// Outcome maps each response to its measurement for a run.
type Outcome map[Response]Measurement

// Response names a measured outcome of a run.
type Response string

// Measurement is a response value of any type.
type Measurement any

// Settings lists possible settings for a factor. A nil or empty slice represents
// a factor that doesn't have a setting.
type Settings []Setting

// UnmarshalYAML accepts a setting or sequence of settings; null and [] have no settings.
func (s *Settings) UnmarshalYAML(node *yaml.Node) error {
	if node.Tag == "!!null" {
		return nil
	}
	if node.Kind == yaml.SequenceNode {
		for _, settingNode := range node.Content {
			var setting Setting
			if err := settingNode.Decode(&setting); err != nil {
				return err
			}
			*s = append(*s, setting)
		}
		return nil
	}
	var setting Setting
	if err := node.Decode(&setting); err != nil {
		return err
	}
	*s = Settings{setting}
	return nil
}

// Setting is a factor value of any type.
type Setting any

// Script is a shell command executed by doe.
type Script string
