package study

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/felixge/doe/internal/model"
)

// Selection identifies a design within its shared study protocol.
type Selection struct {
	Study  *model.Study
	Design *model.Design
}

func (s Selection) String() string { return s.Study.Name + "/" + s.Design.Name }

// Discover loads all top-level *.study.yaml files, in filename order.
func Discover(root string) ([]model.Study, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var studies []model.Study
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".study.yaml") {
			continue
		}
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("study must be a regular file, not a directory or symlink: %s", entry.Name())
		}
		s, err := Load(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		studies = append(studies, s)
	}
	return studies, nil
}

// Resolve validates all selectors and preserves their exact argument order.
func Resolve(studies []model.Study, selectors []string) ([]Selection, error) {
	var available []string
	byName := map[string][]Selection{}
	for i := range studies {
		s := &studies[i]
		for j := range s.Designs {
			selection := Selection{Study: s, Design: &s.Designs[j]}
			available = append(available, selection.String())
			byName[selection.String()] = []Selection{selection}
			byName[selection.Design.Name] = append(byName[selection.Design.Name], selection)
		}
	}
	choices := strings.Join(available, ", ")
	if len(available) == 0 {
		choices = "(none; add a top-level <name>.study.yaml file)"
	}
	if len(selectors) == 0 {
		return nil, fmt.Errorf("at least one design is required; available designs: %s", choices)
	}
	seen := map[string]bool{}
	var selected []Selection
	for _, name := range selectors {
		matches := byName[name]
		if len(matches) == 0 {
			return nil, fmt.Errorf("unknown design %q; available designs: %s", name, choices)
		}
		if len(matches) > 1 {
			var candidates []string
			for _, match := range matches {
				candidates = append(candidates, match.String())
			}
			return nil, fmt.Errorf("ambiguous design %q; use one of: %s", name, strings.Join(candidates, ", "))
		}
		selection := matches[0]
		if seen[selection.String()] {
			return nil, fmt.Errorf("design %q selected more than once", selection.String())
		}
		seen[selection.String()] = true
		selected = append(selected, selection)
	}
	return selected, nil
}
