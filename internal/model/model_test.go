package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"uuid"

	"gopkg.in/yaml.v3"
)

func TestNewExperiment(t *testing.T) {
	study := Study{Factors: Factors{"foo": {1}}, Run: "echo study"}
	experiment := NewExperiment(study)
	if version := experiment.ID[6] >> 4; version != 7 {
		t.Errorf("ID version = %d, want 7", version)
	}
	if experiment.Run != study.Run || !reflect.DeepEqual(experiment.Factors, study.Factors) {
		t.Errorf("experiment = %+v, want factors and run from study %+v", experiment, study)
	}
	if err := experiment.Factors.Set("foo", []byte("[2, 3]")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(study.Factors["foo"], Settings{1}) {
		t.Errorf("study factors changed: %v", study.Factors)
	}
}

func TestDesignReplicates(t *testing.T) {
	var study Study
	if err := yaml.Unmarshal([]byte("factors:\n  foo: [1, 2]\nrun: echo '{}'\nreplicates: 3\n"), &study); err != nil {
		t.Fatal(err)
	}
	experiment := NewExperiment(study)
	if got := experiment.ReplicateCount(); got != 3 {
		t.Errorf("replicate count = %d, want 3", got)
	}
	data, err := json.Marshal(experiment)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Design Design `json:"design"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if got := record.Design.ReplicateCount(); got != 3 {
		t.Errorf("recorded replicate count = %d, want 3: %s", got, data)
	}
	*experiment.Replicates = 9
	if got := study.ReplicateCount(); got != 3 {
		t.Errorf("changing experiment changed study replicate count to %d", got)
	}
	if got := (Design{}).ReplicateCount(); got != 1 {
		t.Errorf("default replicate count = %d, want 1", got)
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
		{"empty settings", Factors{"foo": {}}, nil},
		{"empty with other factors", Factors{"foo": {1}, "bar": nil}, nil},
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

	data, err := json.Marshal(NewExperiment(study))
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
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
}

func TestNewRun(t *testing.T) {
	point := Point{"foo": 42}
	run := NewRun(point)
	if run.ID[6]>>4 != 7 || !reflect.DeepEqual(run.Point, point) || run.Outcome != nil {
		t.Errorf("NewRun = %+v, want UUIDv7 and point %v", run, point)
	}
	if other := NewRun(point); other.ID == run.ID {
		t.Errorf("NewRun reused ID %s", run.ID)
	}
}

func TestRunSerialization(t *testing.T) {
	id := uuid.NewV7()
	point := Point{"foo": 42, "bar": true}
	outcome := Outcome{"sum": 43, "ok": true}
	for _, tc := range []struct {
		name    string
		point   Point
		outcome Outcome
		want    map[string]any
	}{
		{"with settings and measurements", point, outcome, map[string]any{"id": id.String(), "foo": float64(42), "bar": true, "sum": float64(43), "ok": true}},
		{"without settings or measurements", nil, nil, map[string]any{"id": id.String()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(Run{ID: id, Point: tc.point, Outcome: tc.outcome})
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

func TestRunSerializationConflict(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  Run
		want string
	}{
		{"reserved factor", Run{Point: Point{"id": 1}}, `run factor "id" conflicts with reserved field`},
		{"reserved response", Run{Outcome: Outcome{"id": 1}}, `run response "id" conflicts with reserved field`},
		{"factor and response", Run{Point: Point{"foo": 1}, Outcome: Outcome{"foo": 2}}, `run outcome conflicts with factor "foo"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
