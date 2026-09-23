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

// Load decodes a study file into the receiver.
func (s *Study) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var loaded Study
	if err := yaml.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	*s = loaded
	return nil
}

// Design defines the factors and script for an experiment.
type Design struct {
	Factors Factors `json:"factors" yaml:"factors"`
	Run     Script  `json:"run" yaml:"run"`
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
	Design `json:"design"`
}

// NewExperiment creates an experiment from a study with a UUIDv7 ID.
func NewExperiment(study Study) Experiment {
	return Experiment{
		ID:     uuid.NewV7(),
		Design: study.Clone(),
	}
}

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

// Run is a design point identified by a UUID.
// JSON tags are unnecessary because MarshalJSON handles serialization.
type Run struct {
	ID    uuid.UUID
	Point Point
}

// MarshalJSON writes the ID and point settings as a flat object.
func (r Run) MarshalJSON() ([]byte, error) {
	fields := maps.Clone(r.Point)
	if fields == nil {
		fields = make(Point)
	}
	fields["id"] = r.ID
	return json.Marshal(fields)
}

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
