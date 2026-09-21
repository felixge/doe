// Package runner conducts doe studies after command-line parsing.
package runner

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/felixge/doe/internal/cli"
	"github.com/felixge/doe/internal/design"
	"github.com/felixge/doe/internal/model"
	"github.com/felixge/doe/internal/snapshot"
	"github.com/felixge/doe/internal/version"
)

var reserved = map[string]bool{
	"run_id": true, "experiment_id": true, "replicate": true,
	"start": true, "end": true,
}

const resultsMarker = "doe results\n"

// Options contains the validated command-line arguments for doe run.
type Options struct {
	Designs []string
	Plan    bool
	Dirty   bool
	Clean   bool
}

// Execute loads, lists, or conducts the designs selected by doe run.
func Execute(ctx context.Context, env *cli.Env, opts Options) error {
	study, err := loadStudy(opts.Designs)
	if err != nil {
		return err
	}
	if opts.Plan {
		return planStudy(env.Stdout, study)
	}
	if opts.Clean {
		for _, name := range []string{"results", "work"} {
			if err := os.RemoveAll(filepath.Join(study.Root, name)); err != nil {
				return fmt.Errorf("remove %s directory: %w", name, err)
			}
		}
	}

	output := filepath.Join(study.Root, "results")
	if err := ensureOwnedOutput(output); err != nil {
		return err
	}
	if err := validateManagedPaths(output); err != nil {
		return err
	}
	snap, err := snapshot.Capture(study.Root)
	if err != nil {
		return fmt.Errorf("snapshot study: %w", err)
	}

	results, err := loadResults(output)
	if err != nil {
		return err
	}
	dirty := false
	for _, experiment := range results.experiments {
		if experiment.FilesHash != snap.Hash {
			dirty = true
			break
		}
	}
	if dirty && !opts.Dirty {
		return errors.New("study files have changed; use --dirty or clear the results directory")
	}
	if err := writeResultsReadme(output, env.Readme); err != nil {
		return err
	}

	for _, d := range study.Designs {
		if err := conductDesign(ctx, env, study.Root, output, snap, d, results); err != nil {
			return fmt.Errorf("%s: %w", d.Path, err)
		}
	}
	_, err = fmt.Fprintln(env.Stdout, output)
	return err
}

func loadStudy(paths []string) (model.Study, error) {
	var study model.Study
	seen := map[string]bool{}
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return model.Study{}, err
		}
		root, err := canonicalPath(filepath.Dir(absolute))
		if err != nil {
			return model.Study{}, err
		}
		absolute = filepath.Join(root, filepath.Base(absolute))
		info, err := os.Lstat(absolute)
		if err != nil {
			return model.Study{}, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return model.Study{}, fmt.Errorf("design must not be a symlink: %s", path)
		}
		if study.Root == "" {
			study.Root = root
		} else if root != study.Root {
			return model.Study{}, errors.New("all designs must have the same study root")
		}
		name := filepath.Base(absolute)
		if seen[name] {
			return model.Study{}, fmt.Errorf("design %q was specified more than once", name)
		}
		seen[name] = true
		d, err := design.Load(absolute)
		if err != nil {
			return model.Study{}, err
		}
		d.Path = filepath.ToSlash(name)
		study.Designs = append(study.Designs, d)
	}
	return study, nil
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

func ensureOwnedOutput(output string) error {
	info, err := os.Lstat(output)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(output, 0o755); err != nil {
			return err
		}
		return writeMarker(output)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("results path is not a directory: %s", output)
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return writeMarker(output)
	}
	marker := filepath.Join(output, ".doe")
	info, err = os.Lstat(marker)
	if err != nil {
		return fmt.Errorf("refusing to use nonempty directory that is not a doe results directory: %s", output)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("invalid doe results marker: %s", marker)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return err
	}
	if string(data) != resultsMarker {
		return fmt.Errorf("invalid doe results marker: %s", marker)
	}
	return nil
}

func writeMarker(output string) error {
	path := filepath.Join(output, ".doe")
	file, err := os.CreateTemp(output, ".doe-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := io.WriteString(file, resultsMarker); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func validateManagedPaths(output string) error {
	for _, name := range []string{".doe", "README.md", "experiments.jsonl", "runs.jsonl"} {
		info, err := os.Lstat(filepath.Join(output, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlinked results path: %s", filepath.Join(output, name))
		}
	}
	return nil
}

func conductDesign(ctx context.Context, env *cli.Env, root, output string, snap *snapshot.Snapshot, d model.Design, results *resultIndex) error {
	points := d.Points
	started := time.Now()
	environment := map[string]model.Scalar{}
	if d.Setup != "" {
		last, err := commandOutput(ctx, env, root, d.Setup)
		if err != nil {
			return fmt.Errorf("setup: %w", err)
		}
		if last != "" {
			if environment, err = parseFlatObject(last); err != nil {
				return fmt.Errorf("setup result: %w", err)
			}
		}
	}

	experimentID, err := newID()
	if err != nil {
		return err
	}
	experiment := model.Experiment{
		ID: experimentID, Start: started, Design: d.Path,
		Factors: d.FactorNames, Files: snap.Files, FilesHash: snap.Hash,
		Env: environment, EnvHash: objectHash(environment),
	}
	if err := appendJSON(filepath.Join(output, "experiments.jsonl"), experiment); err != nil {
		return err
	}
	results.addExperiment(experiment)
	pointKeys := make([]string, len(points))
	commands := make([]string, len(points))
	for i, point := range points {
		pointKeys[i], err = pointKey(d.Path, point)
		if err != nil {
			return err
		}
		commands[i], err = interpolate(d.Run, point)
		if err != nil {
			return err
		}
	}
	schedule := design.Schedule(len(points), d.Replicates)
	reused := 0
	var doneDuration time.Duration
	for replicate, row := range schedule {
		for _, pointIndex := range row {
			key := replicateKey(pointKeys[pointIndex], replicate+1)
			if duration, ok := results.runs[key]; ok {
				reused++
				doneDuration += duration
			}
		}
	}
	progress := newProgress(env.Stderr, d.Path, len(points)*d.Replicates, reused, doneDuration)
	defer progress.Close()
	runEnv := *env
	runEnv.Stderr = progress

	for replicate, row := range schedule {
		for _, pointIndex := range row {
			point := points[pointIndex]
			key := replicateKey(pointKeys[pointIndex], replicate+1)
			if _, ok := results.runs[key]; ok {
				continue
			}
			runStart := time.Now()
			progress.StartRun()
			last, err := commandOutput(ctx, &runEnv, root, commands[pointIndex])
			if err != nil {
				return fmt.Errorf("replicate %d point #%d: %w", replicate+1, pointIndex+1, err)
			}
			if last == "" {
				return fmt.Errorf("replicate %d point #%d: run produced no result", replicate+1, pointIndex+1)
			}
			outputs, err := parseFlatObject(last)
			if err != nil {
				return fmt.Errorf("replicate %d point #%d result: %w", replicate+1, pointIndex+1, err)
			}
			inputs := pointMap(point)
			for name := range outputs {
				if reserved[name] {
					return fmt.Errorf("response name %q is reserved", name)
				}
				if _, exists := inputs[name]; exists {
					return fmt.Errorf("response name %q is also a factor", name)
				}
			}
			runID, err := newID()
			if err != nil {
				return err
			}
			run := model.Run{
				ID: runID, ExperimentID: experiment.ID, Replicate: replicate + 1,
				Start: runStart, End: time.Now(), Inputs: inputs, Outputs: outputs,
			}
			if err := appendJSON(filepath.Join(output, "runs.jsonl"), flattenRun(run)); err != nil {
				return err
			}
			duration := run.End.Sub(run.Start)
			results.runs[key] = duration
			progress.Complete(duration)
		}
	}
	return nil
}

func commandOutput(ctx context.Context, env *cli.Env, root, script string) (string, error) {
	command := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	command.Dir = root
	command.Stdin = env.Stdin
	stderr := &lockedWriter{writer: env.Stderr}
	command.Stderr = stderr
	configureProcessGroup(command)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := command.Start(); err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	last := ""
	haveLine := false
	for scanner.Scan() {
		if haveLine {
			_, _ = fmt.Fprintln(stderr, last)
		}
		last = scanner.Text()
		haveLine = true
	}
	scanErr := scanner.Err()
	if scanErr != nil {
		_ = stdout.Close()
		_ = command.Cancel()
	}
	waitErr := command.Wait()
	if scanErr != nil {
		return "", scanErr
	}
	if waitErr != nil {
		if haveLine {
			_, _ = fmt.Fprintln(stderr, last)
		}
		return "", waitErr
	}
	if !haveLine {
		return "", nil
	}
	return last, nil
}

type lockedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *lockedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(data)
}

func parseFlatObject(line string) (map[string]model.Scalar, error) {
	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("must be a JSON object")
	}
	if err := ensureEOF(decoder); err != nil {
		return nil, err
	}
	for name, value := range object {
		switch value.(type) {
		case nil, bool, string, json.Number:
		default:
			return nil, fmt.Errorf("field %q must be a JSON scalar", name)
		}
	}
	return object, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("must contain exactly one JSON value")
	}
	return err
}

func interpolate(command string, point model.Point) (string, error) {
	replacements := make([]string, 0, len(point.Values)*2)
	for _, value := range point.Values {
		encoded, err := json.Marshal(value.Value)
		if err != nil {
			return "", err
		}
		text := string(encoded)
		if value.Value != nil {
			if stringValue, ok := value.Value.(string); ok {
				text = stringValue
			}
		}
		replacements = append(replacements, "{"+value.Name+"}", shellQuote(text))
	}
	return strings.NewReplacer(replacements...).Replace(command), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func objectHash(object map[string]model.Scalar) string {
	data, _ := json.Marshal(object)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func pointMap(point model.Point) map[string]model.Scalar {
	values := make(map[string]model.Scalar, len(point.Values))
	for _, value := range point.Values {
		values[value.Name] = value.Value
	}
	return values
}

func flattenRun(run model.Run) map[string]any {
	record := make(map[string]any, len(run.Inputs)+len(run.Outputs)+5)
	record["run_id"] = run.ID
	record["experiment_id"] = run.ExperimentID
	record["replicate"] = run.Replicate
	record["start"] = run.Start
	record["end"] = run.End
	maps.Copy(record, run.Inputs)
	maps.Copy(record, run.Outputs)
	return record
}

func appendJSON(path string, value any) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func writeResultsReadme(output string, source []byte) error {
	if len(source) == 0 {
		return errors.New("embedded README is unavailable")
	}
	build, reproducible := version.Info()
	content := bytes.Replace(source, []byte("@latest"), []byte("@"+build), 1)
	if !reproducible {
		note := []byte("\n> This results snapshot was created by doe " + build + "; the exact doe binary is not reproducible.\n")
		if index := bytes.IndexByte(content, '\n'); index >= 0 {
			content = append(append(append([]byte(nil), content[:index+1]...), note...), content[index+1:]...)
		} else {
			content = append(content, note...)
		}
	}
	file, err := os.CreateTemp(output, ".README-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(output, "README.md"))
}
