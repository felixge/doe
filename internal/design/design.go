// Package design loads designs and derives their points and run schedules.
package design

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strings"

	"github.com/felixge/doe/internal/model"
	"gopkg.in/yaml.v3"
)

// Load reads and validates a design YAML file.
func Load(path string) (model.Design, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.Design{}, err
	}
	return Parse(path, data)
}

// Parse validates design YAML from data. Path is retained on the returned
// design and is only used to make errors and persisted records meaningful.
func Parse(path string, data []byte) (model.Design, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&document); err != nil {
		return model.Design{}, fmt.Errorf("parse %s: %w", path, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return model.Design{}, fmt.Errorf("parse %s: multiple YAML documents", path)
		}
		return model.Design{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return model.Design{}, fmt.Errorf("parse %s: design must be a mapping", path)
	}

	fields, err := mapping(document.Content[0], "design")
	if err != nil {
		return model.Design{}, fmt.Errorf("parse %s: %w", path, err)
	}
	for name, node := range fields {
		switch name {
		case "setup", "factors", "run", "replicates", "concurrency", "concurrency_by":
		default:
			return model.Design{}, nodeError(node, "unknown design field %q", name)
		}
	}

	d := model.Design{Path: path, Replicates: 1, Concurrency: 1}
	if node := fields["setup"]; node != nil {
		if d.Setup, err = stringValue(node, "setup"); err != nil {
			return model.Design{}, err
		}
	}
	if node := fields["run"]; node != nil {
		if d.Run, err = stringValue(node, "run"); err != nil {
			return model.Design{}, err
		}
		if d.Run == "" {
			return model.Design{}, nodeError(node, "run must not be empty")
		}
	} else {
		return model.Design{}, fmt.Errorf("parse %s: missing required field run", path)
	}
	if node := fields["replicates"]; node != nil {
		if d.Replicates, err = positiveInt(node, "replicates"); err != nil {
			return model.Design{}, err
		}
	}
	if node := fields["concurrency"]; node != nil {
		if d.Concurrency, err = positiveInt(node, "concurrency"); err != nil {
			return model.Design{}, err
		}
	}
	if node := fields["factors"]; node != nil {
		groups, names, parseErr := parseFactorGroups(node)
		if parseErr != nil {
			return model.Design{}, parseErr
		}
		d.FactorNames = names
		d.Points, err = expandPoints(groups, names)
		if err != nil {
			return model.Design{}, fmt.Errorf("parse %s: %w", path, err)
		}
	} else {
		return model.Design{}, fmt.Errorf("parse %s: missing required field factors", path)
	}
	if node := fields["concurrency_by"]; node != nil {
		if node.Kind != yaml.SequenceNode {
			return model.Design{}, nodeError(node, "concurrency_by must be a sequence")
		}
		seen := make(map[string]bool, len(node.Content))
		for _, nameNode := range node.Content {
			name, nameErr := stringValue(nameNode, "concurrency_by entry")
			if nameErr != nil {
				return model.Design{}, nameErr
			}
			if !slices.Contains(d.FactorNames, name) {
				return model.Design{}, nodeError(nameNode, "concurrency_by factor %q is not defined", name)
			}
			if seen[name] {
				return model.Design{}, nodeError(nameNode, "duplicate concurrency_by factor %q", name)
			}
			seen[name] = true
			d.ConcurrencyBy = append(d.ConcurrencyBy, name)
		}
	}
	return d, nil
}

type factor struct {
	name     string
	settings []model.Scalar
}

type factorGroup []factor

func parseFactorGroups(node *yaml.Node) ([]factorGroup, []string, error) {
	if node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
		return nil, nil, nodeError(node, "factors must be a non-empty sequence")
	}
	groups := make([]factorGroup, 0, len(node.Content))
	var expected []string
	for groupIndex, groupNode := range node.Content {
		entries, err := mappingEntries(groupNode, fmt.Sprintf("factor group %d", groupIndex+1))
		if err != nil {
			return nil, nil, err
		}
		if len(entries) == 0 {
			return nil, nil, nodeError(groupNode, "factor group %d must not be empty", groupIndex+1)
		}
		group := make(factorGroup, 0, len(entries))
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.key == "" {
				return nil, nil, nodeError(entry.keyNode, "factor name must not be empty")
			}
			if model.IsReservedRunField(entry.key) {
				return nil, nil, nodeError(entry.keyNode, "factor name %q is reserved", entry.key)
			}
			if entry.value.Kind != yaml.SequenceNode || len(entry.value.Content) == 0 {
				return nil, nil, nodeError(entry.value, "settings for factor %q must be a non-empty sequence", entry.key)
			}
			f := factor{name: entry.key, settings: make([]model.Scalar, 0, len(entry.value.Content))}
			for _, settingNode := range entry.value.Content {
				setting, err := scalar(settingNode)
				if err != nil {
					return nil, nil, nodeError(settingNode, "setting for factor %q: %v", entry.key, err)
				}
				f.settings = append(f.settings, setting)
			}
			group = append(group, f)
			names = append(names, entry.key)
		}
		if groupIndex == 0 {
			expected = names
		} else if !sameNames(expected, names) {
			return nil, nil, nodeError(groupNode, "factor group %d must contain the same factors as factor group 1", groupIndex+1)
		}
		groups = append(groups, group)
	}
	return groups, expected, nil
}

func expandPoints(groups []factorGroup, names []string) ([]model.Point, error) {
	points := make([]model.Point, 0)
	seen := make(map[string]int)
	for groupIndex, group := range groups {
		byName := make(map[string]factor, len(group))
		for _, factor := range group {
			byName[factor.name] = factor
		}
		ordered := make([]factor, len(names))
		for i, name := range names {
			ordered[i] = byName[name]
		}
		var expand func(int, []model.Value) error
		expand = func(factorIndex int, values []model.Value) error {
			if factorIndex == len(ordered) {
				point := model.Point{Values: append([]model.Value(nil), values...)}
				key, err := pointKey(point)
				if err != nil {
					return err
				}
				if previous, ok := seen[key]; ok {
					return fmt.Errorf("duplicate design point in factor group %d (already produced by factor group %d)", groupIndex+1, previous)
				}
				seen[key] = groupIndex + 1
				points = append(points, point)
				return nil
			}
			factor := ordered[factorIndex]
			for _, setting := range factor.settings {
				if err := expand(factorIndex+1, append(values, model.Value{Name: factor.name, Value: setting})); err != nil {
					return err
				}
			}
			return nil
		}
		if err := expand(0, make([]model.Value, 0, len(ordered))); err != nil {
			return nil, err
		}
	}
	return points, nil
}

// Schedule returns one row of point indexes per replicate. Replicate 1 uses
// row 0 of a Williams design. A complete design has n rows for even n and 2n
// rows for odd n, and repeats when more replicates are requested.
func Schedule(pointCount, replicates int) [][]int {
	if pointCount <= 0 || replicates <= 0 {
		return nil
	}
	base := make([]int, pointCount)
	for column := range base {
		if column == 0 {
			base[column] = 0
		} else if column%2 == 1 {
			base[column] = (column + 1) / 2
		} else {
			base[column] = pointCount - column/2
		}
	}
	period := pointCount
	if pointCount%2 == 1 && pointCount > 1 {
		period *= 2
	}
	rows := make([][]int, replicates)
	for replicate := range rows {
		designRow := replicate % period
		shift := designRow % pointCount
		reversed := designRow >= pointCount
		row := make([]int, pointCount)
		for column := range row {
			baseColumn := column
			if reversed {
				baseColumn = pointCount - 1 - column
			}
			row[column] = (base[baseColumn] + shift) % pointCount
		}
		rows[replicate] = row
	}
	return rows
}

type mapEntry struct {
	key     string
	keyNode *yaml.Node
	value   *yaml.Node
}

func mapping(node *yaml.Node, description string) (map[string]*yaml.Node, error) {
	entries, err := mappingEntries(node, description)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*yaml.Node, len(entries))
	for _, entry := range entries {
		result[entry.key] = entry.value
	}
	return result, nil
}

func mappingEntries(node *yaml.Node, description string) ([]mapEntry, error) {
	if node.Kind != yaml.MappingNode {
		return nil, nodeError(node, "%s must be a mapping", description)
	}
	entries := make([]mapEntry, 0, len(node.Content)/2)
	seen := make(map[string]bool, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		keyNode, valueNode := node.Content[i], node.Content[i+1]
		if keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
			return nil, nodeError(keyNode, "%s keys must be strings", description)
		}
		if seen[keyNode.Value] {
			return nil, nodeError(keyNode, "duplicate %s key %q", description, keyNode.Value)
		}
		seen[keyNode.Value] = true
		entries = append(entries, mapEntry{key: keyNode.Value, keyNode: keyNode, value: valueNode})
	}
	return entries, nil
}

func stringValue(node *yaml.Node, name string) (string, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", nodeError(node, "%s must be a string", name)
	}
	return node.Value, nil
}

func positiveInt(node *yaml.Node, name string) (int, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
		return 0, nodeError(node, "%s must be a positive integer", name)
	}
	var value int
	if err := node.Decode(&value); err != nil || value <= 0 {
		return 0, nodeError(node, "%s must be a positive integer", name)
	}
	return value, nil
}

func scalar(node *yaml.Node) (model.Scalar, error) {
	if node.Kind != yaml.ScalarNode {
		return nil, errors.New("must be a JSON scalar")
	}
	switch node.Tag {
	case "!!null":
		return nil, nil
	case "!!str":
		return node.Value, nil
	case "!!bool":
		var value bool
		if err := node.Decode(&value); err != nil {
			return nil, errors.New("must be a valid boolean")
		}
		return value, nil
	case "!!int":
		var value any
		if err := node.Decode(&value); err != nil {
			return nil, errors.New("must be a valid integer")
		}
		switch value := value.(type) {
		case int:
			return int64(value), nil
		case int64:
			return value, nil
		case uint64:
			return value, nil
		default:
			return nil, errors.New("integer is outside the supported range")
		}
	case "!!float":
		var value float64
		if err := node.Decode(&value); err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
			return nil, errors.New("must be a finite number")
		}
		return value, nil
	default:
		return nil, errors.New("must be a JSON scalar")
	}
}

func pointKey(point model.Point) (string, error) {
	values := make([]any, len(point.Values))
	for i, value := range point.Values {
		values[i] = value.Value
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode design point: %w", err)
	}
	return string(data), nil
}

func sameNames(want, got []string) bool {
	if len(want) != len(got) {
		return false
	}
	names := make(map[string]bool, len(want))
	for _, name := range want {
		names[name] = true
	}
	for _, name := range got {
		if !names[name] {
			return false
		}
	}
	return true
}

func nodeError(node *yaml.Node, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	if node.Line == 0 {
		return errors.New(message)
	}
	return fmt.Errorf("line %d: %s", node.Line, message)
}
