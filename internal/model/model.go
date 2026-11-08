// Package model defines doe studies and loads them from YAML.
package model

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
	"uuid"

	"gopkg.in/yaml.v3"
)

// Study is the YAML protocol for a single experiment.
type Study struct {
	Design  `yaml:",inline"`
	Presets map[string]Design `yaml:"presets"`
}

// NewStudy creates an empty study.
func NewStudy() Study {
	return Study{}
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

var factorPlaceholder = regexp.MustCompile(`\{[a-zA-Z_][a-zA-Z0-9_]*\}`)

// Design defines the factors and scripts for an experiment.
type Design struct {
	Factors    Factors `json:"factors" yaml:"factors"`
	Setup      Script  `json:"setup,omitempty" yaml:"setup"`
	Run        Script  `json:"run" yaml:"run"`
	Report     Script  `json:"report,omitempty" yaml:"report,omitempty"`
	Replicates int     `json:"replicates" yaml:"replicates"`
}

// Merge applies the nonzero options and factor settings from override to a copy of d.
func (d Design) Merge(override Design) Design {
	d = d.Clone()
	for factor, settings := range override.Factors {
		if d.Factors == nil {
			d.Factors = make(Factors)
		}
		d.Factors[factor] = settings
	}
	d.Setup = cmp.Or(override.Setup, d.Setup)
	d.Run = cmp.Or(override.Run, d.Run)
	d.Report = cmp.Or(override.Report, d.Report)
	d.Replicates = cmp.Or(override.Replicates, d.Replicates)
	return d
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
	for _, factor := range slices.Sorted(maps.Keys(d.Factors)) {
		settings := d.Factors[factor]
		if len(settings) == 0 {
			return fmt.Errorf("factor %q has no settings", factor)
		}
		// Compare stored JSON values, including lists and maps, not shell arguments.
		seen := make(map[string]bool, len(settings))
		for _, setting := range settings {
			data, err := json.Marshal(setting)
			if err != nil {
				return fmt.Errorf("factor %q: encode setting: %w", factor, err)
			}
			key := string(data)
			if seen[key] {
				return fmt.Errorf("factor %q has duplicate setting %s", factor, key)
			}
			seen[key] = true
		}
	}
	placeholders := make(map[Factor]bool)
	for _, placeholder := range factorPlaceholder.FindAllString(string(d.Run), -1) {
		placeholders[Factor(placeholder[1:len(placeholder)-1])] = true
	}
	for _, factor := range slices.Sorted(maps.Keys(d.Factors)) {
		if !placeholders[factor] {
			return fmt.Errorf("run script is missing placeholder {%s} for factor %q", factor, factor)
		}
	}
	return nil
}

// RunHash identifies reusable inputs for one point and one-based replicate.
// JSON sorts map keys. Include the expanded command so runs aren't reused when
// doe changes how placeholders expand. Other points, total replicates, and
// external dependencies are excluded so an extended design can reuse earlier
// runs. For the same reason, setup is hashed before expanding its placeholders
// to all settings. Reporting is also excluded.
func (d Design) RunHash(point Point, replicate int) (string, error) {
	expandedRun, err := d.Run.ExpandPoint(point)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(struct {
		Run         Script `json:"run"`
		Setup       Script `json:"setup"`
		Point       Point  `json:"point"`
		Replicate   int    `json:"replicate"`
		ExpandedRun string `json:"expanded_run"`
	}{d.Run, d.Setup, point, replicate, expandedRun})
	if err != nil {
		return "", fmt.Errorf("hash run inputs: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// Clone returns a design with an independent factors map.
func (d Design) Clone() Design {
	d.Factors = maps.Clone(d.Factors)
	return d
}

// Points returns the cartesian product of the design's factor settings.
// Factors are sorted so the resulting order is stable. If any factor has no
// settings, Points returns an empty slice.
func (d Design) Points() []Point {
	factors := make([]Factor, 0, len(d.Factors))
	for factor := range d.Factors {
		factors = append(factors, factor)
	}
	slices.Sort(factors)

	points := []Point{}
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

// ExperimentState describes the recorded lifecycle of an experiment.
type ExperimentState string

const (
	ExperimentStateSetup     ExperimentState = "setup"
	ExperimentStateRunning   ExperimentState = "running"
	ExperimentStateReporting ExperimentState = "reporting"
	ExperimentStateDone      ExperimentState = "done"
	ExperimentStateStopped   ExperimentState = "stopped"
	ExperimentStateError     ExperimentState = "error"
)

// Experiment is a single invocation of a study. Its ID is a UUIDv7.
type Experiment struct {
	ID       uuid.UUID `json:"id"`
	Design   `json:"design"`
	Points   []Point         `json:"points"`
	Preset   string          `json:"preset,omitempty"`
	Env      Env             `json:"env"`
	State    ExperimentState `json:"state"`
	Start    time.Time       `json:"start,omitzero"`
	SetupEnd time.Time       `json:"setup_end,omitzero"`
	End      time.Time       `json:"end,omitzero"`
	Error    string          `json:"error,omitempty"`
}

// NewExperiment creates an experiment from a design with a UUIDv7 ID.
func NewExperiment(design Design) Experiment {
	design = design.Clone()
	return Experiment{
		ID:     uuid.NewV7(),
		Start:  time.Now(),
		Env:    Env{},
		State:  ExperimentStateSetup,
		Points: design.Points(),
		Design: design,
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

// String formats a point as CLI-style factor=YAML pairs on one line.
func (p Point) String() string {
	pairs := make([]string, 0, len(p))
	for _, factor := range slices.Sorted(maps.Keys(p)) {
		var node yaml.Node
		if err := node.Encode(p[factor]); err != nil {
			continue
		}
		flowYAML(&node)
		value, err := yaml.Marshal(&node)
		if err != nil {
			continue
		}
		pairs = append(pairs, string(factor)+"="+strings.TrimSuffix(string(value), "\n"))
	}
	return strings.Join(pairs, " ")
}

func flowYAML(node *yaml.Node) {
	if node.Kind == yaml.MappingNode || node.Kind == yaml.SequenceNode {
		node.Style = yaml.FlowStyle
	}
	if node.Kind == yaml.ScalarNode && strings.ContainsAny(node.Value, "\r\n") {
		node.Style = yaml.DoubleQuotedStyle
	}
	for _, child := range node.Content {
		flowYAML(child)
	}
}

// RunState describes the recorded lifecycle of a run.
type RunState string

const (
	RunStateRunning RunState = "running"
	RunStateDone    RunState = "done"
	RunStateStopped RunState = "stopped"
	RunStateError   RunState = "error"
)

// Run is an execution of a design point, identified by a UUID.
type Run struct {
	ID                   uuid.UUID `json:"id"`
	ExperimentID         uuid.UUID `json:"experiment_id"`                   // Experiment containing this record.
	OriginalExperimentID uuid.UUID `json:"original_experiment_id,omitzero"` // Execution origin; zero for fresh runs.
	Point                Point     `json:"point"`
	Replicate            int       `json:"replicate"`
	State                RunState  `json:"state"`
	Start                time.Time `json:"start,omitzero"`
	End                  time.Time `json:"end,omitzero"`
	Error                string    `json:"error,omitempty"`
	Outcome              Outcome   `json:"outcome,omitzero"`
}

// Reused reports whether the run was copied from an earlier experiment.
func (r Run) Reused() bool {
	return r.OriginalExperimentID != (uuid.UUID{})
}

// NewRun creates a run for an experiment, design point, and one-based replicate with a UUIDv7 ID.
func NewRun(experimentID uuid.UUID, point Point, replicate int) *Run {
	return &Run{
		ID: uuid.NewV7(), ExperimentID: experimentID, Point: point,
		Replicate: replicate, State: RunStateRunning, Start: time.Now(),
	}
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

// ExpandPoint replaces factor placeholders with the point's settings.
func (s Script) ExpandPoint(point Point) (string, error) {
	return s.expand(func(factor Factor) (string, bool, error) {
		setting, ok := point[factor]
		if !ok {
			return "", false, nil
		}
		arg, err := settingArg(setting)
		return arg, true, err
	})
}

// ExpandFactors replaces factor placeholders with JSON arrays of the factors'
// settings, e.g. for setup, which runs once for all points.
func (s Script) ExpandFactors(factors Factors) (string, error) {
	return s.expand(func(factor Factor) (string, bool, error) {
		settings, ok := factors[factor]
		if !ok {
			return "", false, nil
		}
		data, err := marshalJSON(settings)
		return string(data), true, err
	})
}

// expand replaces each placeholder that arg resolves with one shell argument.
// Unresolved placeholders are kept, e.g. the {HOME} in ${HOME}.
func (s Script) expand(arg func(Factor) (string, bool, error)) (string, error) {
	var err error
	expanded := factorPlaceholder.ReplaceAllStringFunc(string(s), func(placeholder string) string {
		value, ok, argErr := arg(Factor(placeholder[1 : len(placeholder)-1]))
		if !ok {
			return placeholder
		}
		if argErr != nil {
			if err == nil {
				err = fmt.Errorf("expand %s: %w", placeholder, argErr)
			}
			return placeholder
		}
		return shellQuote(value)
	})
	return expanded, err
}

// settingArg formats a setting like jq -r: values that encode as JSON strings
// as-is, e.g. strings and timestamps, and all other values as JSON.
func settingArg(setting Setting) (string, error) {
	data, err := marshalJSON(setting)
	if err != nil {
		return "", err
	}
	if data[0] != '"' {
		return string(data), nil
	}
	var s string
	err = json.Unmarshal(data, &s)
	return s, err
}

// marshalJSON encodes v as JSON without escaping <, >, and & for HTML.
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), nil
}

// shellSafe matches arguments that need no quoting in a POSIX shell.
var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+:,./-]+$`)

// shellQuote quotes s as a single POSIX shell argument unless it is safe as-is,
// so that common settings such as names, numbers, and paths stay readable.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
