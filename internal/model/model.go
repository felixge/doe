// Package model defines doe studies and loads them from YAML.
package model

import (
	"fmt"
	"maps"
	"os"
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
		ID:      uuid.NewV7(),
		Factors: maps.Clone(study.Factors),
		Run:     study.Run,
	}
}

// Factors maps each factor to its possible settings.
type Factors map[Factor]Settings

// Set parses a YAML setting or sequence and replaces a factor's settings.
func (f *Factors) Set(name Factor, value []byte) error {
	var settings Settings
	if err := yaml.Unmarshal(value, &settings); err != nil {
		return fmt.Errorf("factor %q: %w", name, err)
	}
	if *f == nil {
		*f = make(Factors)
	}
	(*f)[name] = settings
	return nil
}

// Factor names an input to a run.
type Factor string

// Settings lists possible values for a factor. A nil or empty slice represents
// a factor that doesn't have a setting.
type Settings []Setting

// UnmarshalYAML accepts a value or sequence of values; null and [] have no settings.
func (s *Settings) UnmarshalYAML(node *yaml.Node) error {
	if node.Tag == "!!null" {
		return nil
	}
	if node.Kind == yaml.SequenceNode {
		for _, setting := range node.Content {
			var value Setting
			if err := setting.Decode(&value); err != nil {
				return err
			}
			*s = append(*s, value)
		}
		return nil
	}
	var value Setting
	if err := node.Decode(&value); err != nil {
		return err
	}
	*s = Settings{value}
	return nil
}

// Setting is a factor value of any type.
type Setting any

// Script is a shell command executed by doe.
type Script string
