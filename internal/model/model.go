// Package model defines doe studies and loads them from YAML.
package model

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Factor names an input to a run.
type Factor string

// Setting is a factor value passed to a run as a string.
type Setting string

// Settings is one or more possible values for a factor.
type Settings []Setting

// Study is the YAML protocol for a single experiment.
type Study struct {
	Factors map[Factor]Settings `yaml:"factors"`
	Run     string              `yaml:"run"`
}

// UnmarshalYAML accepts one scalar or a non-empty sequence of scalars.
func (s *Settings) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.SequenceNode {
		if len(node.Content) == 0 {
			return fmt.Errorf("settings must not be empty")
		}
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
	*s = Settings{Setting(node.Value)}
	return nil
}

// Set parses a YAML setting or sequence and replaces a factor's settings.
func (s *Study) Set(name Factor, value string) error {
	var settings Settings
	if err := yaml.Unmarshal([]byte(value), &settings); err != nil {
		return fmt.Errorf("factor %q: %w", name, err)
	}
	if len(settings) == 0 {
		return fmt.Errorf("factor %q: setting is empty", name)
	}
	if s.Factors == nil {
		s.Factors = make(map[Factor]Settings)
	}
	s.Factors[name] = settings
	return nil
}

// Load decodes a study file into a Study.
func Load(path string) (Study, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Study{}, err
	}
	var s Study
	if err := yaml.Unmarshal(data, &s); err != nil {
		return Study{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}
