// Package study loads YAML studies and applies command-line factors.
package study

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Study is the YAML protocol for a single experiment.
type Study struct {
	Factors Factors `yaml:"factors"`
	Run     string  `yaml:"run"`
}

// Factors retains YAML declaration order for deterministic design points.
type Factors struct {
	Names    []string
	Settings map[string][]string
}

// Set parses a YAML setting or sequence and replaces a factor's settings.
func (f *Factors) Set(name, value string) error {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(value), &document); err != nil {
		return fmt.Errorf("factor %q: %w", name, err)
	}
	if len(document.Content) == 0 {
		return fmt.Errorf("factor %q: setting is empty", name)
	}
	settings, err := parseSettings(document.Content[0])
	if err != nil {
		return fmt.Errorf("factor %q: %w", name, err)
	}
	if f.Settings == nil {
		f.Settings = make(map[string][]string)
	}
	if _, exists := f.Settings[name]; !exists {
		f.Names = append(f.Names, name)
	}
	f.Settings[name] = settings
	return nil
}

// UnmarshalYAML decodes the factors mapping in its original order.
func (f *Factors) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("factors must be a mapping")
	}
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Tag != "!!str" {
			return fmt.Errorf("factor names must be strings")
		}
		if _, exists := f.Settings[key.Value]; exists {
			return fmt.Errorf("duplicate factor %q", key.Value)
		}
		settings, err := parseSettings(value)
		if err != nil {
			return fmt.Errorf("factor %q: %w", key.Value, err)
		}
		if f.Settings == nil {
			f.Settings = make(map[string][]string)
		}
		f.Names = append(f.Names, key.Value)
		f.Settings[key.Value] = settings
	}
	return nil
}

func parseSettings(node *yaml.Node) ([]string, error) {
	if node.Kind == yaml.SequenceNode {
		if len(node.Content) == 0 {
			return nil, fmt.Errorf("settings must not be empty")
		}
		settings := make([]string, 0, len(node.Content))
		for _, child := range node.Content {
			setting, err := scalar(child)
			if err != nil {
				return nil, err
			}
			settings = append(settings, setting)
		}
		return settings, nil
	}
	setting, err := scalar(node)
	if err != nil {
		return nil, err
	}
	return []string{setting}, nil
}

func scalar(node *yaml.Node) (string, error) {
	if node.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("settings must be YAML scalars or a sequence of scalars")
	}
	return node.Value, nil
}

// Load decodes a study file into a Study.
func Load(path string) (Study, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Study{}, err
	}
	var s Study
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&s); err != nil {
		return Study{}, fmt.Errorf("parse %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return Study{}, fmt.Errorf("parse %s: multiple YAML documents", path)
	} else if !errors.Is(err, io.EOF) {
		return Study{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}
