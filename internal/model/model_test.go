package model

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"uuid"

	"gopkg.in/yaml.v3"
)

func TestNewExperiment(t *testing.T) {
	study := Study{Factors: Factors{"foo": {1}}, Run: "echo study"}
	experiment, err := NewExperiment(study)
	if err != nil {
		t.Fatal(err)
	}
	if experiment.PID != os.Getpid() || experiment.ProcessStartTime == "" {
		t.Errorf("process identity = (%d, %q), want current PID and start time", experiment.PID, experiment.ProcessStartTime)
	}
	other, err := NewExperiment(study)
	if err != nil {
		t.Fatal(err)
	}
	if other.ProcessStartTime != experiment.ProcessStartTime {
		t.Errorf("process start changed: %q != %q", other.ProcessStartTime, experiment.ProcessStartTime)
	}
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

	experiment, err := NewExperiment(study)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(experiment)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record["pid"] != float64(os.Getpid()) || record["process_start_time"] != experiment.ProcessStartTime {
		t.Errorf("experiment has incorrect process identity: %s", data)
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

func TestRunSerialization(t *testing.T) {
	id := uuid.NewV7()
	point := Point{"foo": 42, "bar": true, "id": "factor value"}
	for _, tc := range []struct {
		name  string
		point Point
		want  map[string]any
	}{
		{"with settings", point, map[string]any{"id": id.String(), "foo": float64(42), "bar": true}},
		{"without settings", nil, map[string]any{"id": id.String()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(Run{ID: id, Point: tc.point})
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
	if point["id"] != "factor value" {
		t.Errorf("marshaling changed point: %v", point)
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
