package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/felixge/doe/internal/cli"
	"github.com/felixge/doe/internal/model"
	"github.com/felixge/doe/internal/study"
)

var errPlanWrite = errors.New("plan write failed")

type failingWriter struct {
	failAt int
	writes int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes >= w.failAt {
		return 0, errPlanWrite
	}
	return len(p), nil
}

func TestWriteTableOutput(t *testing.T) {
	var output bytes.Buffer
	if err := writeTable(&output, [][]string{{"#", "value"}, {"1", "x"}}); err != nil {
		t.Fatal(err)
	}
	const want = "+---+-------+\n| # | value |\n+---+-------+\n| 1 | x     |\n+---+-------+\n"
	if got := output.String(); got != want {
		t.Fatalf("writeTable() output = %q, want %q", got, want)
	}
}

func TestWriteTableStopsOnWriteError(t *testing.T) {
	writer := &failingWriter{failAt: 4}
	err := writeTable(writer, [][]string{{"#", "value"}, {"1", "x"}})
	if !errors.Is(err, errPlanWrite) {
		t.Fatalf("writeTable() error = %v, want %v", err, errPlanWrite)
	}
	if writer.writes != writer.failAt {
		t.Fatalf("writes after failure: got %d, want %d", writer.writes, writer.failAt)
	}
}

func TestPlanDesignsOutput(t *testing.T) {
	var output bytes.Buffer
	s := model.Study{Name: "compression", Designs: []model.Design{{
		Name:        "full",
		FactorNames: []string{"value"},
		Points: []model.Point{
			{Values: []model.Value{{Name: "value", Value: "one"}}},
			{Values: []model.Value{{Name: "value", Value: "two"}}},
		},
		Replicates: 2,
	}}}
	if err := planDesigns(&output, []study.Selection{{Study: &s, Design: &s.Designs[0]}}, emptyExecutions(s.Name)); err != nil {
		t.Fatal(err)
	}
	const want = "compression/full\nDesign points:\n" +
		"+-------+-------+\n| point | value |\n+-------+-------+\n| #1    | one   |\n| #2    | two   |\n+-------+-------+\n\n" +
		"Schedule:\n" +
		"+---------+----+----+\n| run/rep | 1  | 2  |\n+---------+----+----+\n| 1       | #1 | #2 |\n| 2       | #2 | #1 |\n+---------+----+----+\n\n" +
		"Runs: 4; reusable: 0; new: 4\n\nTotal runs: 4; reusable: 0; new: 4\n* Reusable from existing results for the same design.\n"
	if got := output.String(); got != want {
		t.Fatalf("planDesigns() output = %q, want %q", got, want)
	}
}

func TestPlanDesignsConcurrencyGroups(t *testing.T) {
	var output bytes.Buffer
	s := model.Study{Name: "compression", Designs: []model.Design{{
		Name:          "full",
		FactorNames:   []string{"host", "size"},
		Concurrency:   2,
		ConcurrencyBy: []string{"host"},
		Points: []model.Point{
			{Values: []model.Value{{Name: "host", Value: "a"}, {Name: "size", Value: int64(1)}}},
			{Values: []model.Value{{Name: "host", Value: "b"}, {Name: "size", Value: int64(1)}}},
			{Values: []model.Value{{Name: "host", Value: "a"}, {Name: "size", Value: int64(2)}}},
			{Values: []model.Value{{Name: "host", Value: "b"}, {Name: "size", Value: int64(2)}}},
		},
		Replicates: 4,
	}}}
	if err := planDesigns(&output, []study.Selection{{Study: &s, Design: &s.Designs[0]}}, emptyExecutions(s.Name)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"| point | host | size |",
		"Concurrency: 2 per group, 2 groups, 4 maximum active runs",
		`Schedule (host=a):
+---------+----+----+----+----+
| run/rep | 1  | 2  | 3  | 4  |
+---------+----+----+----+----+
| 1       | #1 | #3 | #3 | #1 |
| 2       | #3 | #1 | #1 | #3 |
+---------+----+----+----+----+`,
		`Schedule (host=b):
+---------+----+----+----+----+
| run/rep | 1  | 2  | 3  | 4  |
+---------+----+----+----+----+
| 1       | #2 | #2 | #4 | #4 |
| 2       | #4 | #4 | #2 | #2 |
+---------+----+----+----+----+`,
		"Total runs: 16",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("output does not contain %q:\n%s", want, output.String())
		}
	}
}

func TestPlanDesignsReturnsWriteError(t *testing.T) {
	writer := &failingWriter{failAt: 1}
	s := model.Study{Name: "compression", Designs: []model.Design{{
		Name:        "full",
		FactorNames: []string{"value"},
		Points:      []model.Point{{Values: []model.Value{{Name: "value", Value: "x"}}}},
		Replicates:  1,
	}}}
	if err := planDesigns(writer, []study.Selection{{Study: &s, Design: &s.Designs[0]}}, emptyExecutions(s.Name)); !errors.Is(err, errPlanWrite) {
		t.Fatalf("planDesigns() error = %v, want %v", err, errPlanWrite)
	}
	if writer.writes != 1 {
		t.Fatalf("writes after failure: got %d, want 1", writer.writes)
	}
}

func TestExecutePlanReturnsWriteError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "compression.study.yaml"), overlappingStudy)
	writer := &failingWriter{failAt: 1}
	env := &cli.Env{Stdin: bytes.NewReader(nil), Stdout: writer, Stderr: io.Discard}
	err := Execute(context.Background(), env, Options{
		Project: root,
		Designs: []string{"full"},
		Plan:    true,
	})
	if !errors.Is(err, errPlanWrite) {
		t.Fatalf("Execute() error = %v, want %v", err, errPlanWrite)
	}
}

func emptyExecutions(name string) map[string]*studyExecution {
	return map[string]*studyExecution{name: {results: &resultIndex{runs: map[string]time.Duration{}}}}
}

func TestPlanReuseOnlyFromSameDesign(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "compression.study.yaml"), overlappingStudy)
	opts := Options{Project: root, Designs: []string{"smoke", "full"}, Plan: true}
	stdout, stderr := newBuffers()
	if err := Execute(context.Background(), testEnv(stdout, stderr), opts); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Runs: 2; reusable: 0; new: 2", "Runs: 6; reusable: 0; new: 6", "Total runs: 8; reusable: 0; new: 8"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("plan missing %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "results")); !os.IsNotExist(err) {
		t.Fatal("plan created results")
	}
	opts.Plan = false
	opts.Designs = []string{"smoke"}
	if err := Execute(context.Background(), testEnv(new(bytes.Buffer), stderr), opts); err != nil {
		t.Fatal(err)
	}
	opts.Plan = true
	opts.Designs = []string{"smoke", "full"}
	stdout.Reset()
	if err := Execute(context.Background(), testEnv(stdout, stderr), opts); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Runs: 2; reusable: 2; new: 0", "Runs: 6; reusable: 0; new: 6", "Total runs: 8; reusable: 2; new: 6", "#1*", "#2*"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("plan missing %q:\n%s", want, stdout)
		}
	}
	if got := lineCount(t, filepath.Join(root, "results", "compression", "experiments.jsonl")); got != 1 {
		t.Fatalf("plan created experiment: %d", got)
	}
}
