package study

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/felixge/doe/internal/model"
	"gopkg.in/yaml.v3"
)

func TestParseAndExpandPoints(t *testing.T) {
	s, err := Parse("project/compression.study.yaml", []byte(`
setup: ./setup.bash
run: ./run.bash {file} {level}
designs:
  full:
    factors:
      file: [a.txt, b.txt]
      level: [1, 2.5, 3]
      enabled: [true]
      note: [null]
    replicates: 7
    concurrency: 3
    concurrency_by: [file, enabled]
`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "compression" || s.Path != "compression.study.yaml" || s.Setup != "./setup.bash" || s.Run != "./run.bash {file} {level}" {
		t.Fatalf("study = %+v", s)
	}
	d := s.Designs[0]
	if d.Name != "full" || d.Replicates != 7 || d.Concurrency != 3 || !reflect.DeepEqual(d.ConcurrencyBy, []string{"file", "enabled"}) {
		t.Fatalf("design = %+v", d)
	}
	if got, want := d.FactorNames, []string{"file", "level", "enabled", "note"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("FactorNames = %v, want %v", got, want)
	}

	points := d.Points
	want := []model.Point{
		{Values: []model.Value{{Name: "file", Value: "a.txt"}, {Name: "level", Value: int64(1)}, {Name: "enabled", Value: true}, {Name: "note", Value: nil}}},
		{Values: []model.Value{{Name: "file", Value: "a.txt"}, {Name: "level", Value: 2.5}, {Name: "enabled", Value: true}, {Name: "note", Value: nil}}},
		{Values: []model.Value{{Name: "file", Value: "a.txt"}, {Name: "level", Value: int64(3)}, {Name: "enabled", Value: true}, {Name: "note", Value: nil}}},
		{Values: []model.Value{{Name: "file", Value: "b.txt"}, {Name: "level", Value: int64(1)}, {Name: "enabled", Value: true}, {Name: "note", Value: nil}}},
		{Values: []model.Value{{Name: "file", Value: "b.txt"}, {Name: "level", Value: 2.5}, {Name: "enabled", Value: true}, {Name: "note", Value: nil}}},
		{Values: []model.Value{{Name: "file", Value: "b.txt"}, {Name: "level", Value: int64(3)}, {Name: "enabled", Value: true}, {Name: "note", Value: nil}}},
	}
	if !reflect.DeepEqual(points, want) {
		t.Fatalf("Points() = %#v\nwant %#v", points, want)
	}
}

func TestParseDefaultsReplicates(t *testing.T) {
	d, err := parseDesignYAML("factors: {a: [x]}")
	if err != nil {
		t.Fatal(err)
	}
	if d.Replicates != 1 || d.Concurrency != 1 || d.ConcurrencyBy != nil {
		t.Fatalf("defaults = replicates %d, concurrency %d, concurrency_by %v", d.Replicates, d.Concurrency, d.ConcurrencyBy)
	}
}

func TestParseRejectsInvalidDesigns(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		message string
	}{
		{"not mapping", "- run\n", "design must be a mapping"},
		{"unknown field", "factors: {a: [x]}\nextra: true\n", "unknown design field \"extra\""},
		{"shared run", "factors: {a: [x]}\nrun: ok\n", "unknown design field \"run\""},
		{"shared setup", "factors: {a: [x]}\nsetup: ok\n", "unknown design field \"setup\""},
		{"duplicate field", "replicates: 1\nreplicates: 2\nfactors: {a: [x]}\n", "duplicate design key \"replicates\""},
		{"missing factors", "{}", "missing required field factors"},
		{"empty factors", "factors: {}\n", "factors must not be empty"},
		{"sequence factors", "factors: [{a: [x]}]\n", "factors must be a mapping"},
		{"empty factor name", "factors:\n  '': [x]\n", "factor name must not be empty"},
		{"reserved factor", "factors:\n  run_id: [x]\n", "factor name \"run_id\" is reserved"},
		{"duplicate factor", "factors:\n  a: [x]\n  a: [y]\n", "duplicate factors key \"a\""},
		{"empty settings", "factors: {a: []}\n", "settings for factor \"a\" must be a non-empty sequence"},
		{"scalar settings", "factors: {a: x}\n", "settings for factor \"a\" must be a non-empty sequence"},
		{"object setting", "factors: {a: [{nested: value}]}\n", "setting for factor \"a\": must be a JSON scalar"},
		{"array setting", "factors: {a: [[nested]]}\n", "setting for factor \"a\": must be a JSON scalar"},
		{"infinite setting", "factors: {a: [.inf]}\n", "setting for factor \"a\": must be a finite number"},
		{"zero replicates", "factors: {a: [x]}\nreplicates: 0\n", "replicates must be a positive integer"},
		{"float replicates", "factors: {a: [x]}\nreplicates: 1.5\n", "replicates must be a positive integer"},
		{"zero concurrency", "factors: {a: [x]}\nconcurrency: 0\n", "concurrency must be a positive integer"},
		{"concurrency by wrong type", "factors: {a: [x]}\nconcurrency_by: a\n", "concurrency_by must be a sequence"},
		{"unknown concurrency factor", "factors: {a: [x]}\nconcurrency_by: [b]\n", "concurrency_by factor \"b\" is not defined"},
		{"duplicate concurrency factor", "factors: {a: [x]}\nconcurrency_by: [a, a]\n", "duplicate concurrency_by factor \"a\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseDesignYAML(tt.yaml)
			if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("Parse() error = %v, want containing %q", err, tt.message)
			}
		})
	}
}

func TestParseRejectsDuplicateSettings(t *testing.T) {
	for _, settings := range []string{"[x, x]", "[1, 1.0]", "[false, false]", "[null, null]"} {
		t.Run(settings, func(t *testing.T) {
			_, err := parseDesignYAML("factors: {a: " + settings + "}")
			if err == nil || !strings.Contains(err.Error(), `duplicate setting for factor "a"`) {
				t.Fatalf("Parse() error = %v, want duplicate setting", err)
			}
		})
	}
}

func TestParsePreservesDistinctScalarSettings(t *testing.T) {
	d, err := parseDesignYAML(`factors: {a: [1, "1", true, "true", null, "null"]}`)
	if err != nil {
		t.Fatal(err)
	}
	var got []model.Scalar
	for _, point := range d.Points {
		got = append(got, point.Values[0].Value)
	}
	if want := []model.Scalar{int64(1), "1", true, "true", nil, "null"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("settings=%#v want=%#v", got, want)
	}
}

func TestParsedPointValuesAreIndependent(t *testing.T) {
	d, err := parseDesignYAML("factors: {a: [x, y], b: [1]}\n")
	if err != nil {
		t.Fatal(err)
	}
	d.Points[0].Values[0].Value = "changed"
	if got, want := d.Points[1].Values[0].Value, "y"; got != want {
		t.Fatalf("mutating point 1 changed point 2: got %v, want %v", got, want)
	}
}

func parseDesignYAML(text string) (model.Design, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(text), &document); err != nil {
		return model.Design{}, err
	}
	return parseDesign("full", document.Content[0])
}

func TestScheduleEven(t *testing.T) {
	got := Schedule(4, 6)
	want := [][]int{
		{0, 1, 3, 2},
		{1, 2, 0, 3},
		{2, 3, 1, 0},
		{3, 0, 2, 1},
		{0, 1, 3, 2},
		{1, 2, 0, 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Schedule() = %v, want %v", got, want)
	}
	assertBalancedPositions(t, got[:4])
	assertBalancedCarryover(t, got[:4])
}

func TestScheduleOdd(t *testing.T) {
	got := Schedule(3, 8)
	want := [][]int{
		{0, 1, 2},
		{1, 2, 0},
		{2, 0, 1},
		{2, 1, 0},
		{0, 2, 1},
		{1, 0, 2},
		{0, 1, 2},
		{1, 2, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Schedule() = %v, want %v", got, want)
	}
	assertBalancedPositions(t, got[:6])
	assertBalancedCarryover(t, got[:6])
}

func TestScheduleLargeSingleReplicate(t *testing.T) {
	const pointCount = 100_000
	rows := Schedule(pointCount, 1)
	if len(rows) != 1 {
		t.Fatalf("Schedule(%d, 1) row count = %d, want 1", pointCount, len(rows))
	}
	if len(rows[0]) != pointCount {
		t.Fatalf("Schedule(%d, 1) column count = %d, want %d", pointCount, len(rows[0]), pointCount)
	}
	if got, want := rows[0][:6], []int{0, 1, pointCount - 1, 2, pointCount - 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Schedule(%d, 1) prefix = %v, want %v", pointCount, got, want)
	}
}

func TestScheduleRowsAreIndependent(t *testing.T) {
	rows := Schedule(3, 7)
	rows[0][0] = -1
	if got, want := rows[6][0], 0; got != want {
		t.Fatalf("mutating row 0 changed repeated row 6: got %d, want %d", got, want)
	}
}

func TestScheduleInvalidOrSinglePoint(t *testing.T) {
	if got := Schedule(0, 2); got != nil {
		t.Fatalf("Schedule(0, 2) = %v, want nil", got)
	}
	if got := Schedule(2, 0); got != nil {
		t.Fatalf("Schedule(2, 0) = %v, want nil", got)
	}
	if got, want := Schedule(1, 3), [][]int{{0}, {0}, {0}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Schedule(1, 3) = %v, want %v", got, want)
	}
}

func TestCompleteSchedulesAreBalanced(t *testing.T) {
	for pointCount := 2; pointCount <= 9; pointCount++ {
		t.Run(strconv.Itoa(pointCount), func(t *testing.T) {
			rowCount := pointCount
			if pointCount%2 == 1 {
				rowCount *= 2
			}
			rows := Schedule(pointCount, rowCount)
			assertBalancedPositions(t, rows)
			assertBalancedCarryover(t, rows)
		})
	}
}

func assertBalancedPositions(t *testing.T, rows [][]int) {
	t.Helper()
	n := len(rows[0])
	counts := make([][]int, n)
	for point := range counts {
		counts[point] = make([]int, n)
	}
	for _, row := range rows {
		for position, point := range row {
			counts[point][position]++
		}
	}
	for point := 1; point < n; point++ {
		if !reflect.DeepEqual(counts[point], counts[0]) {
			t.Fatalf("position counts are not balanced: %v", counts)
		}
	}
}

func assertBalancedCarryover(t *testing.T, rows [][]int) {
	t.Helper()
	n := len(rows[0])
	counts := make([][]int, n)
	for previous := range counts {
		counts[previous] = make([]int, n)
	}
	for _, row := range rows {
		for i := 1; i < len(row); i++ {
			counts[row[i-1]][row[i]]++
		}
	}
	want := -1
	for previous := range n {
		for next := range n {
			if previous == next {
				continue
			}
			if want < 0 {
				want = counts[previous][next]
			}
			if counts[previous][next] != want {
				t.Fatalf("carryover counts are not balanced: %v", counts)
			}
		}
	}
}
