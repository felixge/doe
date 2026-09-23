// Package study loads YAML studies and applies command-line factors.
package study

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Study is the YAML protocol for a single experiment.
type Study struct {
	Factors map[string]Settings `yaml:"factors"`
	Run     string              `yaml:"run"`
}

// Settings contains the string representations passed to the shell for a factor.
type Settings []string

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
			*s = append(*s, setting.Value)
		}
		return nil
	}
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("settings must be YAML scalars or a sequence of scalars")
	}
	*s = Settings{node.Value}
	return nil
}

// Set parses a YAML setting or sequence and replaces a factor's settings.
func (s *Study) Set(name, value string) error {
	var settings Settings
	if err := yaml.Unmarshal([]byte(value), &settings); err != nil {
		return fmt.Errorf("factor %q: %w", name, err)
	}
	if len(settings) == 0 {
		return fmt.Errorf("factor %q: setting is empty", name)
	}
	if s.Factors == nil {
		s.Factors = make(map[string]Settings)
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
