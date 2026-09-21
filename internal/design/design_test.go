package design

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/felixge/doe/internal/model"
)

func TestParseAndExpandPoints(t *testing.T) {
	d, err := Parse("study/design.yaml", []byte(`
setup: ./setup.bash
factors:
  - file: [a.txt, b.txt]
    level: [1, 2.5]
    enabled: [true]
    note: [null]
  - level: [3]
    note: [later]
    file: [c.txt]
    enabled: [false]
run: ./run.bash {file} {level}
replicates: 7
`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Path != "study/design.yaml" || d.Setup != "./setup.bash" || d.Run != "./run.bash {file} {level}" || d.Replicates != 7 {
		t.Fatalf("design = %+v", d)
	}
	if got, want := FactorNames(d), []string{"file", "level", "enabled", "note"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("FactorNames() = %v, want %v", got, want)
	}

	points, err := Points(d)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Point{
		{Values: []model.Value{{Name: "file", Value: "a.txt"}, {Name: "level", Value: int64(1)}, {Name: "enabled", Value: true}, {Name: "note", Value: nil}}},
		{Values: []model.Value{{Name: "file", Value: "a.txt"}, {Name: "level", Value: 2.5}, {Name: "enabled", Value: true}, {Name: "note", Value: nil}}},
		{Values: []model.Value{{Name: "file", Value: "b.txt"}, {Name: "level", Value: int64(1)}, {Name: "enabled", Value: true}, {Name: "note", Value: nil}}},
		{Values: []model.Value{{Name: "file", Value: "b.txt"}, {Name: "level", Value: 2.5}, {Name: "enabled", Value: true}, {Name: "note", Value: nil}}},
		{Values: []model.Value{{Name: "file", Value: "c.txt"}, {Name: "level", Value: int64(3)}, {Name: "enabled", Value: false}, {Name: "note", Value: "later"}}},
	}
	if !reflect.DeepEqual(points, want) {
		t.Fatalf("Points() = %#v\nwant %#v", points, want)
	}
}

func TestParseDefaultsReplicates(t *testing.T) {
	d, err := Parse("design.yaml", []byte("factors:\n  - a: [x]\nrun: echo ok\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Replicates != 1 {
		t.Fatalf("Replicates = %d, want 1", d.Replicates)
	}
}

func TestParseRejectsInvalidDesigns(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		message string
	}{
		{"not mapping", "- run\n", "design must be a mapping"},
		{"multiple documents", "factors: [{a: [x]}]\nrun: ok\n---\nfactors: [{a: [y]}]\nrun: ok\n", "multiple YAML documents"},
		{"unknown field", "factors: [{a: [x]}]\nrun: ok\nextra: true\n", "unknown design field \"extra\""},
		{"duplicate field", "run: first\nrun: second\nfactors: [{a: [x]}]\n", "duplicate design key \"run\""},
		{"missing run", "factors: [{a: [x]}]\n", "missing required field run"},
		{"empty run", "factors: [{a: [x]}]\nrun: ''\n", "run must not be empty"},
		{"run wrong type", "factors: [{a: [x]}]\nrun: [no]\n", "run must be a string"},
		{"missing factors", "run: ok\n", "missing required field factors"},
		{"empty factors", "factors: []\nrun: ok\n", "factors must be a non-empty sequence"},
		{"empty group", "factors: [{}]\nrun: ok\n", "factor group 1 must not be empty"},
		{"empty factor name", "factors:\n  - '': [x]\nrun: ok\n", "factor name must not be empty"},
		{"reserved factor", "factors:\n  - run_id: [x]\nrun: ok\n", "factor name \"run_id\" is reserved"},
		{"duplicate factor", "factors:\n  - a: [x]\n    a: [y]\nrun: ok\n", "duplicate factor group 1 key \"a\""},
		{"empty settings", "factors: [{a: []}]\nrun: ok\n", "settings for factor \"a\" must be a non-empty sequence"},
		{"object setting", "factors: [{a: [{nested: value}]}]\nrun: ok\n", "setting for factor \"a\": must be a JSON scalar"},
		{"array setting", "factors: [{a: [[nested]]}]\nrun: ok\n", "setting for factor \"a\": must be a JSON scalar"},
		{"infinite setting", "factors: [{a: [.inf]}]\nrun: ok\n", "setting for factor \"a\": must be a finite number"},
		{"different factors", "factors:\n  - a: [x]\n    b: [y]\n  - a: [z]\nrun: ok\n", "factor group 2 must contain the same factors"},
		{"zero replicates", "factors: [{a: [x]}]\nrun: ok\nreplicates: 0\n", "replicates must be a positive integer"},
		{"float replicates", "factors: [{a: [x]}]\nrun: ok\nreplicates: 1.5\n", "replicates must be a positive integer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("design.yaml", []byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("Parse() error = %v, want containing %q", err, tt.message)
			}
		})
	}
}

func TestParseRejectsDuplicatePoints(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "within group",
			yaml: "factors: [{a: [x, x]}]\nrun: ok\n",
		},
		{
			name: "across groups with reordered factors",
			yaml: "factors:\n  - a: [x]\n    b: [1]\n  - b: [1.0]\n    a: [x]\nrun: ok\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("design.yaml", []byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), "duplicate design point") {
				t.Fatalf("Parse() error = %v, want duplicate design point", err)
			}
		})
	}
}

func TestPointsRejectsNonScalarProgrammaticSetting(t *testing.T) {
	d := model.Design{Factors: []model.FactorGroup{{Factors: []model.Factor{{Name: "a", Settings: []model.Scalar{map[string]string{"not": "scalar"}}}}}}}
	_, err := Points(d)
	if err == nil || !strings.Contains(err.Error(), "not a JSON scalar") {
		t.Fatalf("Points() error = %v", err)
	}

	d.Factors[0].Factors[0].Settings = []model.Scalar{math.Inf(1)}
	_, err = Points(d)
	if err == nil || !strings.Contains(err.Error(), "not a JSON scalar") {
		t.Fatalf("Points() error for infinity = %v", err)
	}
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
