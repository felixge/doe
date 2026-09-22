package study

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/felixge/doe/internal/model"
)

func TestParseStudyValidation(t *testing.T) {
	const designs = "designs:\n  smoke: {factors: [{a: [1], b: [2]}]}\n"
	for _, test := range []struct{ name, yaml, want string }{
		{"not mapping", "[]", "study must be a mapping"},
		{"missing run", designs, "missing required field run"},
		{"empty run", "run: ''\n" + designs, "run must not be empty"},
		{"run type", "run: []\n" + designs, "run must be a string"},
		{"setup type", "setup: []\nrun: ok\n" + designs, "setup must be a string"},
		{"unknown field", "run: ok\nfactors: []\n" + designs, "unknown study field"},
		{"duplicate run", "run: ok\nrun: again\n" + designs, "duplicate study key"},
		{"missing designs", "run: ok", "missing required field designs"},
		{"empty designs", "run: ok\ndesigns: {}", "designs must not be empty"},
		{"designs type", "run: ok\ndesigns: []", "designs must be a mapping"},
		{"duplicate design", "run: ok\n" + designs + "  smoke: {factors: [{a: [3], b: [4]}]}\n", "duplicate designs key"},
		{"different factors", "run: ok\n" + designs + "  full: {factors: [{a: [3], c: [4]}]}\n", "same factor names"},
		{"multiple documents", "run: ok\n" + designs + "---\nrun: ok\n" + designs, "multiple YAML documents"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse("compression.study.yaml", []byte(test.yaml))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want %q", err, test.want)
			}
		})
	}
	// A different declaration order is allowed; the factor names form a set.
	s, err := Parse("compression.study.yaml", []byte("run: ok\n"+designs+"  full: {factors: [{b: [3], a: [4]}]}\n"))
	if err != nil || len(s.Designs) != 2 {
		t.Fatalf("study=%+v err=%v", s, err)
	}
	for _, name := range []string{"Upper", "under_score", "has/slash", "-leading", "trailing-", "two--hyphens", ""} {
		if !strings.Contains(name, "/") {
			if _, err := Parse(name+".study.yaml", []byte("run: ok\n"+designs)); err == nil {
				t.Errorf("accepted study name %q", name)
			}
		}
		if _, err := Parse("compression.study.yaml", []byte("run: ok\ndesigns:\n  '"+name+"': {factors: [{a: [1]}]}\n")); err == nil {
			t.Errorf("accepted design name %q", name)
		}
	}
	if _, err := Parse("design.yaml", []byte("run: ok\n"+designs)); err == nil {
		t.Fatal("accepted standalone design YAML")
	}
}

func TestDiscoverAndResolve(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		text := "run: echo '{}'\ndesigns:\n  smoke: {factors: [{value: [1]}]}\n"
		if name == "a" {
			text += "  full: {factors: [{value: [1, 2]}]}\n"
		}
		if err := os.WriteFile(filepath.Join(root, name+".study.yaml"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "ignored.yaml"), []byte("invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "ignored.study.yaml"), []byte("invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	studies, err := Discover(root)
	if err != nil || len(studies) != 2 {
		t.Fatalf("studies=%+v err=%v", studies, err)
	}
	selected, err := Resolve(studies, []string{"a/smoke", "b/smoke", "full"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, selection := range selected {
		names = append(names, selection.String())
	}
	if !reflect.DeepEqual(names, []string{"a/smoke", "b/smoke", "a/full"}) {
		t.Fatalf("names=%v", names)
	}
	for _, test := range []struct {
		names []string
		want  []string
	}{
		{nil, []string{"at least one design", "a/smoke", "a/full", "b/smoke"}},
		{[]string{"unknown"}, []string{"unknown design", "a/smoke", "a/full", "b/smoke"}},
		{[]string{"smoke"}, []string{"ambiguous", "a/smoke", "b/smoke"}},
		{[]string{"full", "a/full"}, []string{"more than once", "a/full"}},
	} {
		_, err := Resolve(studies, test.names)
		for _, want := range test.want {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error=%v want %q", err, want)
			}
		}
	}
	if err := os.Symlink("a.study.yaml", filepath.Join(root, "link.study.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error=%v", err)
	}
}

func TestResolveEmptyProject(t *testing.T) {
	if _, err := Resolve([]model.Study{}, nil); err == nil || !strings.Contains(err.Error(), "add a top-level") {
		t.Fatalf("error=%v", err)
	}
}
