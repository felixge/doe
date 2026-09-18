// Package design loads designs and derives their points and run schedules.
package design

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/felixge/doe/internal/model"
	"gopkg.in/yaml.v3"
)

var reservedFactorNames = map[string]bool{
	"run_id":        true,
	"experiment_id": true,
	"replicate":     true,
	"start":         true,
	"end":           true,
}

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
		case "setup", "factors", "run", "replicates":
		default:
			return model.Design{}, nodeError(node, "unknown design field %q", name)
		}
	}

	d := model.Design{Path: path, Replicates: 1}
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
	if node := fields["factors"]; node != nil {
		if d.Factors, err = parseFactorGroups(node); err != nil {
			return model.Design{}, err
		}
	} else {
		return model.Design{}, fmt.Errorf("parse %s: missing required field factors", path)
	}

	if _, err := Points(d); err != nil {
		return model.Design{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return d, nil
}

func parseFactorGroups(node *yaml.Node) ([]model.FactorGroup, error) {
	if node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
		return nil, nodeError(node, "factors must be a non-empty sequence")
	}
	groups := make([]model.FactorGroup, 0, len(node.Content))
	var expected []string
	for groupIndex, groupNode := range node.Content {
		entries, err := mappingEntries(groupNode, fmt.Sprintf("factor group %d", groupIndex+1))
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			return nil, nodeError(groupNode, "factor group %d must not be empty", groupIndex+1)
		}
		group := model.FactorGroup{Factors: make([]model.Factor, 0, len(entries))}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.key == "" {
				return nil, nodeError(entry.keyNode, "factor name must not be empty")
			}
			if reservedFactorNames[entry.key] {
				return nil, nodeError(entry.keyNode, "factor name %q is reserved", entry.key)
			}
			if entry.value.Kind != yaml.SequenceNode || len(entry.value.Content) == 0 {
				return nil, nodeError(entry.value, "settings for factor %q must be a non-empty sequence", entry.key)
			}
			factor := model.Factor{Name: entry.key, Settings: make([]model.Scalar, 0, len(entry.value.Content))}
			for _, settingNode := range entry.value.Content {
				setting, err := scalar(settingNode)
				if err != nil {
					return nil, nodeError(settingNode, "setting for factor %q: %v", entry.key, err)
				}
				factor.Settings = append(factor.Settings, setting)
			}
			group.Factors = append(group.Factors, factor)
			names = append(names, entry.key)
		}
		if groupIndex == 0 {
			expected = names
		} else if !sameNames(expected, names) {
			return nil, nodeError(groupNode, "factor group %d must contain the same factors as factor group 1", groupIndex+1)
		}
		groups = append(groups, group)
	}
	return groups, nil
}

// FactorNames returns the canonical factor order established by the first
// factor group.
func FactorNames(d model.Design) []string {
	if len(d.Factors) == 0 {
		return nil
	}
	names := make([]string, len(d.Factors[0].Factors))
	for i, factor := range d.Factors[0].Factors {
		names[i] = factor.Name
	}
	return names
}

// Points expands each factor group into its Cartesian product and concatenates
// the products in declaration order. The last declared factor varies fastest.
func Points(d model.Design) ([]model.Point, error) {
	names := FactorNames(d)
	if len(names) == 0 {
		return nil, errors.New("design has no factors")
	}
	points := make([]model.Point, 0)
	seen := make(map[string]int)
	for groupIndex, group := range d.Factors {
		byName := make(map[string]model.Factor, len(group.Factors))
		for _, factor := range group.Factors {
			byName[factor.Name] = factor
		}
		ordered := make([]model.Factor, len(names))
		for i, name := range names {
			factor, ok := byName[name]
			if !ok {
				return nil, fmt.Errorf("factor group %d is missing factor %q", groupIndex+1, name)
			}
			if len(factor.Settings) == 0 {
				return nil, fmt.Errorf("factor %q has no settings", name)
			}
			ordered[i] = factor
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
			for _, setting := range factor.Settings {
				if !validScalar(setting) {
					return fmt.Errorf("setting for factor %q is not a JSON scalar", factor.Name)
				}
				if err := expand(factorIndex+1, append(values, model.Value{Name: factor.Name, Value: setting})); err != nil {
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
	complete := make([][]int, 0, pointCount*2)
	for shift := 0; shift < pointCount; shift++ {
		row := make([]int, pointCount)
		for column, point := range base {
			row[column] = (point + shift) % pointCount
		}
		complete = append(complete, row)
	}
	if pointCount%2 == 1 && pointCount > 1 {
		for shift := 0; shift < pointCount; shift++ {
			row := make([]int, pointCount)
			for column := range row {
				row[column] = complete[shift][pointCount-1-column]
			}
			complete = append(complete, row)
		}
	}
	rows := make([][]int, replicates)
	for replicate := range rows {
		rows[replicate] = append([]int(nil), complete[replicate%len(complete)]...)
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

func validScalar(value model.Scalar) bool {
	switch value := value.(type) {
	case nil, bool, string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return true
	case float32:
		return !math.IsInf(float64(value), 0) && !math.IsNaN(float64(value))
	case float64:
		return !math.IsInf(value, 0) && !math.IsNaN(value)
	default:
		return false
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
	want = append([]string(nil), want...)
	got = append([]string(nil), got...)
	sort.Strings(want)
	sort.Strings(got)
	return reflect.DeepEqual(want, got)
}

func nodeError(node *yaml.Node, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	if node.Line == 0 {
		return errors.New(message)
	}
	return fmt.Errorf("line %d: %s", node.Line, message)
}
