package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/felixge/doe/internal/cli"
	"github.com/felixge/doe/internal/model"
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

func TestPlanStudyOutput(t *testing.T) {
	var output bytes.Buffer
	study := model.Study{Designs: []model.Design{{
		FactorNames: []string{"value"},
		Points: []model.Point{
			{Values: []model.Value{{Name: "value", Value: "one"}}},
			{Values: []model.Value{{Name: "value", Value: "two"}}},
		},
		Replicates: 2,
	}}}
	if err := planStudy(&output, study); err != nil {
		t.Fatal(err)
	}
	const want = "Design points:\n" +
		"+---+-------+\n| # | value |\n+---+-------+\n| 1 | one   |\n| 2 | two   |\n+---+-------+\n\n" +
		"Schedule:\n" +
		"+-----------+---+---+\n| replicate | 1 | 2 |\n+-----------+---+---+\n| 1         | 1 | 2 |\n| 2         | 2 | 1 |\n+-----------+---+---+\n\n" +
		"Total runs: 4\n"
	if got := output.String(); got != want {
		t.Fatalf("planStudy() output = %q, want %q", got, want)
	}
}

func TestPlanStudyReturnsWriteError(t *testing.T) {
	writer := &failingWriter{failAt: 1}
	study := model.Study{Designs: []model.Design{{
		FactorNames: []string{"value"},
		Points:      []model.Point{{Values: []model.Value{{Name: "value", Value: "x"}}}},
		Replicates:  1,
	}}}
	if err := planStudy(writer, study); !errors.Is(err, errPlanWrite) {
		t.Fatalf("planStudy() error = %v, want %v", err, errPlanWrite)
	}
	if writer.writes != 1 {
		t.Fatalf("writes after failure: got %d, want 1", writer.writes)
	}
}

func TestExecutePlanReturnsWriteError(t *testing.T) {
	root := t.TempDir()
	designPath := filepath.Join(root, "design.yaml")
	writeFile(t, designPath, "factors: [{value: [x]}]\nrun: echo '{}'\n")
	writer := &failingWriter{failAt: 1}
	env := &cli.Env{Stdin: bytes.NewReader(nil), Stdout: writer, Stderr: io.Discard}
	err := Execute(context.Background(), env, Options{
		Designs: []string{designPath},
		Plan:    true,
	})
	if !errors.Is(err, errPlanWrite) {
		t.Fatalf("Execute() error = %v, want %v", err, errPlanWrite)
	}
}
