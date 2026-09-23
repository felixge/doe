// Package model defines doe studies and loads them from YAML.
package model

import (
	"fmt"
	"os"
	"uuid"

	"gopkg.in/yaml.v3"
)

// Study is the YAML protocol for a single experiment.
type Study struct {
	Factors map[Factor]Settings `yaml:"factors"`
	Run     Script              `yaml:"run"`
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

// SetFactor parses a YAML setting or sequence and replaces a factor's settings.
func (s *Study) SetFactor(name Factor, value string) error {
	var settings Settings
	if err := yaml.Unmarshal([]byte(value), &settings); err != nil {
		return fmt.Errorf("factor %q: %w", name, err)
	}
	if s.Factors == nil {
		s.Factors = make(map[Factor]Settings)
	}
	s.Factors[name] = settings
	return nil
}

// Execution is a single invocation of a study. Its ID is a UUIDv7, which
// embeds a timestamp and sorts by creation time unless the clock moves backwards.
type Execution struct {
	ID uuid.UUID
	Study
}

// NewExecution creates an execution with a UUIDv7 ID.
func NewExecution() Execution {
	return Execution{ID: uuid.NewV7()}
}

// Factor names an input to a run.
type Factor string

// Settings lists possible values for a factor. A nil or empty slice represents
// a factor that doesn't have a setting.
type Settings []Setting

// UnmarshalYAML accepts a scalar or sequence; null and [] have no settings.
func (s *Settings) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.SequenceNode {
		for _, setting := range node.Content {
			if setting.Kind != yaml.ScalarNode {
				return fmt.Errorf("settings must be YAML scalars")
			}
			*s = append(*s, Setting(setting.Value))
		}
		return nil
	}
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("settings must be YAML scalars or a sequence of scalars")
	}
	if node.Tag == "!!null" {
		return nil
	}
	*s = Settings{Setting(node.Value)}
	return nil
}

// Setting is a factor value passed to a run as a string.
type Setting string

// Script is a shell command executed by doe.
type Script string
