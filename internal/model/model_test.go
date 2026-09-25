package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
	"uuid"

	"gopkg.in/yaml.v3"
)

func TestPointString(t *testing.T) {
	for _, tc := range []struct {
		name  string
		point Point
		want  string
	}{
		{"scalars", Point{"z": true, "b": "true", "a": "hello world", "n": 3.5}, `a=hello world b="true" n=3.5 z=true`},
		{"collections", Point{"list": []any{1, "two"}, "map": map[string]any{"nested": []any{true, nil}}}, `list=[1, two] map={nested: [true, null]}`},
		{"multiline", Point{"text": "one\ntwo"}, `text="one\ntwo"`},
		{"empty", Point{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.point.String(); got != tc.want {
				t.Errorf("Point.String() = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestNewExperiment(t *testing.T) {
	study := Study{Factors: Factors{"foo": {1}}, Run: "echo study"}
	before := time.Now()
	experiment := NewExperiment(study.Design)
	after := time.Now()
	if experiment.Start.Before(before) || experiment.Start.After(after) {
		t.Errorf("experiment start = %s; want within [%s, %s]", experiment.Start, before, after)
	}
	if version := experiment.ID[6] >> 4; version != 7 {
		t.Errorf("ID version = %d, want 7", version)
	}
	if experiment.Env == nil || len(experiment.Env) != 0 {
		t.Errorf("Env = %v, want empty map", experiment.Env)
	}
	if experiment.Run != study.Run || !reflect.DeepEqual(experiment.Factors, study.Factors) {
		t.Errorf("experiment = %+v, want factors and run from study %+v", experiment, study)
	}
	if !reflect.DeepEqual(experiment.Points, []Point{{"foo": 1}}) {
		t.Errorf("points = %v, want [{foo:1}]", experiment.Points)
	}
	if err := experiment.Factors.Set("foo", []byte("[2, 3]")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(study.Factors["foo"], Settings{1}) {
		t.Errorf("study factors changed: %v", study.Factors)
	}
}

func TestDesignMerge(t *testing.T) {
	base := Design{Factors: Factors{"foo": {1}, "bar": {2}}, Setup: "setup", Run: "run", Replicates: 3}
	preset := Design{Factors: Factors{"foo": {4}, "baz": {5}}, Run: "preset run"}
	cli := Design{Factors: Factors{"bar": {6}}, Replicates: 2}
	merged := base.Merge(preset).Merge(cli)
	want := Design{Factors: Factors{"foo": {4}, "bar": {6}, "baz": {5}}, Setup: "setup", Run: "preset run", Replicates: 2}
	if !reflect.DeepEqual(merged, want) {
		t.Errorf("merged design = %+v, want %+v", merged, want)
	}
	if !reflect.DeepEqual(base.Factors, Factors{"foo": {1}, "bar": {2}}) {
		t.Errorf("merge changed base factors: %v", base.Factors)
	}
	if got := base.Merge(Design{Replicates: 0}).Replicates; got != 3 {
		t.Errorf("zero replicate override = %d, want 3", got)
	}
}

func TestDesignValidate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		design Design
		want   string
	}{
		{"valid", Design{Factors: Factors{"foo": {1}}, Run: "echo '{foo}'", Replicates: 2}, ""},
		{"multiple factors", Design{Factors: Factors{"foo": {1}, "bar": {2}}, Run: "echo {foo} {bar}", Replicates: 1}, ""},
		{"missing placeholder", Design{Factors: Factors{"foo": {1}}, Run: "echo '{}'", Replicates: 1}, `missing placeholder {foo}`},
		{"missing one of two", Design{Factors: Factors{"foo": {1}, "bar": {2}}, Run: "echo {foo}", Replicates: 1}, `missing placeholder {bar}`},
		{"partial name", Design{Factors: Factors{"foo": {1}}, Run: "echo {foobar}", Replicates: 1}, `missing placeholder {foo}`},
		{"invalid placeholder syntax", Design{Factors: Factors{"foo-bar": {1}}, Run: "echo {foo-bar}", Replicates: 1}, `missing placeholder {foo-bar}`},
		{"no factors", Design{Run: "echo '{}'", Replicates: 1}, "at least one factor is required"},
		{"empty settings", Design{Factors: Factors{"foo": {}}, Run: "echo '{}'", Replicates: 1}, `factor "foo" has no settings`},
		{"empty run", Design{Factors: Factors{"foo": {1}}, Replicates: 1}, "run script is required"},
		{"zero replicates", Design{Factors: Factors{"foo": {1}}, Run: "echo '{}'"}, "replicates must be a positive integer"},
		{"negative replicates", Design{Factors: Factors{"foo": {1}}, Run: "echo '{}'", Replicates: -2}, "replicates must be a positive integer"},
		{"reserved error factor", Design{Factors: Factors{"error": {1}}, Run: "echo '{}'", Replicates: 1}, `run factor "error" conflicts with reserved field`},
		{"reserved start factor", Design{Factors: Factors{"start": {1}}, Run: "echo '{}'", Replicates: 1}, `run factor "start" conflicts with reserved field`},
		{"reserved end factor", Design{Factors: Factors{"end": {1}}, Run: "echo '{}'", Replicates: 1}, `run factor "end" conflicts with reserved field`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.design.Validate()
			if tc.want == "" {
				if err != nil {
					t.Errorf("Validate() = %v, want nil", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDesignPoints(t *testing.T) {
	design := Design{Factors: Factors{
		"foo": {1, 2, 3},
		"bar": {4, 5},
	}}
	want := []Point{
		{"bar": 4, "foo": 1},
		{"bar": 4, "foo": 2},
		{"bar": 4, "foo": 3},
		{"bar": 5, "foo": 1},
		{"bar": 5, "foo": 2},
		{"bar": 5, "foo": 3},
	}
	points := design.Points()
	if !reflect.DeepEqual(points, want) {
		t.Fatalf("points = %v, want %v", points, want)
	}
	points[0]["foo"] = 99
	if points[1]["foo"] != 2 || design.Factors["foo"][0] != 1 {
		t.Errorf("modifying a point changed another point or the design: %v, %v", points, design.Factors)
	}
}

func TestDesignPointsEmpty(t *testing.T) {
	for _, tc := range []struct {
		name    string
		factors Factors
		want    []Point
	}{
		{"no factors", nil, []Point{{}}},
		{"empty settings", Factors{"foo": {}}, []Point{}},
		{"empty with other factors", Factors{"foo": {1}, "bar": nil}, []Point{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Design{Factors: tc.factors}).Points(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("points = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDesignSerialization(t *testing.T) {
	var study Study
	if err := yaml.Unmarshal([]byte("factors:\n  foo: 1\nrun: echo study\n"), &study); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(study.Factors["foo"], Settings{1}) || study.Run != "echo study" {
		t.Errorf("study = %+v, want flat YAML design", study)
	}

	experiment := NewExperiment(study.Design)
	data, err := json.Marshal(experiment)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record["start"] != experiment.Start.Format(time.RFC3339Nano) {
		t.Errorf("experiment start = %v, want %s", record["start"], experiment.Start.Format(time.RFC3339Nano))
	}
	if _, ok := record["setup_end"]; ok {
		t.Errorf("experiment without completed setup has setup_end: %s", data)
	}
	experiment.SetupEnd = experiment.Start.Add(time.Second)
	data, err = json.Marshal(experiment)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record["setup_end"] != experiment.SetupEnd.Format(time.RFC3339Nano) {
		t.Errorf("experiment setup_end = %v, want %s", record["setup_end"], experiment.SetupEnd.Format(time.RFC3339Nano))
	}
	if _, ok := record["factors"]; ok {
		t.Errorf("experiment has top-level factors: %s", data)
	}
	if _, ok := record["run"]; ok {
		t.Errorf("experiment has top-level run: %s", data)
	}
	if design, ok := record["design"].(map[string]any); !ok || design["run"] != "echo study" || design["factors"] == nil {
		t.Errorf("experiment has no nested design: %s", data)
	}
	if _, ok := record["preset"]; ok {
		t.Errorf("experiment without preset records one: %s", data)
	}
	if setupError, ok := record["setup_error"]; !ok || setupError != "" {
		t.Errorf("experiment without setup error omits its column: %s", data)
	}
	if !reflect.DeepEqual(record["points"], []any{map[string]any{"foo": float64(1)}}) {
		t.Errorf("experiment has wrong points: %s", data)
	}
}

func TestNewExperimentEmptyPoints(t *testing.T) {
	experiment := NewExperiment(Design{Factors: Factors{"foo": {}}})
	data, err := json.Marshal(experiment)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Points []Point `json:"points"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Points == nil || len(record.Points) != 0 {
		t.Errorf("points = %s, want []", data)
	}
}

func TestNewRun(t *testing.T) {
	point := Point{"foo": 42}
	experimentID := uuid.NewV7()
	before := time.Now()
	run := NewRun(experimentID, point, 2)
	after := time.Now()
	if run.ID[6]>>4 != 7 || run.ExperimentID != experimentID || !reflect.DeepEqual(run.Point, point) || run.Replicate != 2 || run.Outcome != nil {
		t.Errorf("NewRun = %+v, want UUIDv7 and point %v", run, point)
	}
	if run.Start.Before(before) || run.Start.After(after) || !run.End.IsZero() {
		t.Errorf("NewRun timing = %s to %s; want start within [%s, %s] and no end", run.Start, run.End, before, after)
	}
	if other := NewRun(experimentID, point, 2); other.ID == run.ID {
		t.Errorf("NewRun reused ID %s", run.ID)
	}
}

func TestRunSerialization(t *testing.T) {
	id := uuid.NewV7()
	experimentID := uuid.NewV7()
	point := Point{"foo": 42, "bar": true}
	outcome := Outcome{"sum": 43, "ok": true}
	start := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	end := start.Add(1500 * time.Millisecond)
	for _, tc := range []struct {
		name    string
		point   Point
		outcome Outcome
		want    map[string]any
	}{
		{"with settings and measurements", point, outcome, map[string]any{"id": id.String(), "experiment_id": experimentID.String(), "replicate": float64(2), "start": start.Format(time.RFC3339Nano), "end": end.Format(time.RFC3339Nano), "error": "", "foo": float64(42), "bar": true, "sum": float64(43), "ok": true}},
		{"without settings or measurements", nil, nil, map[string]any{"id": id.String(), "experiment_id": experimentID.String(), "replicate": float64(2), "start": start.Format(time.RFC3339Nano), "end": end.Format(time.RFC3339Nano), "error": ""}},
		{"formerly reserved fields", Point{"failed": true}, Outcome{"exit_code": 7}, map[string]any{"id": id.String(), "experiment_id": experimentID.String(), "replicate": float64(2), "start": start.Format(time.RFC3339Nano), "end": end.Format(time.RFC3339Nano), "error": "", "failed": true, "exit_code": float64(7)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := &Run{ID: id, ExperimentID: experimentID, Point: tc.point, Replicate: 2, Start: start, End: end, Outcome: tc.outcome}
			if err := run.Valid(); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(run)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("run JSON = %s, want %v", data, tc.want)
			}
		})
	}
	if point["foo"] != 42 || outcome["sum"] != 43 {
		t.Errorf("marshaling changed point or outcome: %v, %v", point, outcome)
	}
}

func TestFailedRunSerialization(t *testing.T) {
	run := &Run{Point: Point{"foo": 1}, Error: "exit status 7"}
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Foo   int    `json:"foo"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &got); err != nil || got.Foo != 1 || got.Error != "exit status 7" {
		t.Errorf("failed run JSON = %s, %v", data, err)
	}
	run.Error = ""
	data, err = json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil || got.Error != "" || !strings.Contains(string(data), `"error":""`) {
		t.Errorf("empty error column should be present: %s, %v", data, err)
	}
}

func TestRunSerializationConflict(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  *Run
		want string
	}{
		{"reserved factor", &Run{Point: Point{"id": 1}}, `run factor "id" conflicts with reserved field`},
		{"replicate factor", &Run{Point: Point{"replicate": 1}}, `run factor "replicate" conflicts with reserved field`},
		{"experiment ID factor", &Run{Point: Point{"experiment_id": 1}}, `run factor "experiment_id" conflicts with reserved field`},
		{"error factor", &Run{Point: Point{"error": 1}}, `run factor "error" conflicts with reserved field`},
		{"start factor", &Run{Point: Point{"start": 1}}, `run factor "start" conflicts with reserved field`},
		{"end factor", &Run{Point: Point{"end": 1}}, `run factor "end" conflicts with reserved field`},
		{"reserved response", &Run{Outcome: Outcome{"id": 1}}, `run response "id" conflicts with reserved field`},
		{"error response", &Run{Outcome: Outcome{"error": 1}}, `run response "error" conflicts with reserved field`},
		{"start response", &Run{Outcome: Outcome{"start": 1}}, `run response "start" conflicts with reserved field`},
		{"end response", &Run{Outcome: Outcome{"end": 1}}, `run response "end" conflicts with reserved field`},
		{"replicate response", &Run{Outcome: Outcome{"replicate": 1}}, `run response "replicate" conflicts with reserved field`},
		{"experiment ID response", &Run{Outcome: Outcome{"experiment_id": 1}}, `run response "experiment_id" conflicts with reserved field`},
		{"factor and response", &Run{Point: Point{"foo": 1}, Outcome: Outcome{"foo": 2}}, `run outcome conflicts with factor "foo"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run.Valid(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Valid() = %v, want %q", err, tc.want)
			}
			_, err := json.Marshal(tc.run)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("conflicting run JSON error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFactorsSet(t *testing.T) {
	var factors Factors
	if err := factors.Set("foo", []byte("42")); err != nil {
		t.Fatal(err)
	}
	if err := factors.Set("bar", []byte("[true, false]")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(factors["foo"], Settings{42}) || !reflect.DeepEqual(factors["bar"], Settings{true, false}) {
		t.Errorf("factors = %v", factors)
	}
	if err := factors.Set("foo", []byte("[bad")); err == nil || !strings.Contains(err.Error(), `factor "foo"`) {
		t.Errorf("invalid YAML error = %v", err)
	}
	if !reflect.DeepEqual(factors["foo"], Settings{42}) {
		t.Errorf("invalid YAML changed settings: %v", factors["foo"])
	}
}
