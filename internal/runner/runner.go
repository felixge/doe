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
	"github.com/felixge/doe/internal/model"
	"github.com/felixge/doe/internal/snapshot"
	"github.com/felixge/doe/internal/study"
	"golang.org/x/term"
)

const resultsMarker = "doe results\n"

// Options contains the validated command-line arguments for doe run.
type Options struct {
	Project string
	Designs []string
	Plan    bool
	Dirty   bool
}

type studyExecution struct {
	snapshot   *snapshot.Snapshot
	results    *resultIndex
	designs    []string
	experiment model.Experiment
}

// Execute loads, lists, or conducts the designs selected by doe run.
func Execute(ctx context.Context, env *cli.Env, opts Options) error {
	root, err := canonicalPath(opts.Project)
	if err != nil {
		return err
	}
	studies, err := study.Discover(root)
	if err != nil {
		return err
	}
	selected, err := study.Resolve(studies, opts.Designs)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(filepath.Join(root, "results")); err == nil {
		if !info.IsDir() {
			return errors.New("results path must be a directory, not a file or symlink")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// Preflight is read-only, including loading partial JSONL tails.
	executions := map[string]*studyExecution{}
	for _, selection := range selected {
		s := selection.Study
		if execution := executions[s.Name]; execution != nil {
			execution.designs = append(execution.designs, selection.Design.Name)
			continue
		}
		output := filepath.Join(root, "results", s.Name)
		if err := checkOwnedOutput(output); err != nil {
			return err
		}
		if err := validateManagedPaths(output); err != nil {
			return err
		}
		snap, err := snapshot.Capture(root, s.Path)
		if err != nil {
			return fmt.Errorf("snapshot %s: %w", s.Name, err)
		}
		results, err := loadResults(output)
		if err != nil {
			return err
		}
		for _, experiment := range results.experiments {
			if experiment.FilesHash != snap.Hash && !opts.Dirty {
				return fmt.Errorf("study %s files have changed; use --dirty or clear the results directory %s", s.Name, output)
			}
		}
		executions[s.Name] = &studyExecution{snapshot: snap, results: results, designs: []string{selection.Design.Name}}
	}
	if opts.Plan {
		return planDesigns(env.Stdout, selected, executions)
	}
	for _, selection := range selected {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := selection.Study
		execution := executions[s.Name]
		output := filepath.Join(root, "results", s.Name)
		if execution.experiment.ID == "" {
			if err := beginExperiment(ctx, env, root, output, *s, execution); err != nil {
				return fmt.Errorf("%s: %w", s.Name, err)
			}
		}
		if err := conductDesign(ctx, env, root, output, s.Run, *selection.Design, execution.experiment, execution.results); err != nil {
			return fmt.Errorf("%s: %w", selection, err)
		}
	}
	return nil
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

func checkOwnedOutput(output string) error {
	info, err := os.Lstat(output)
	if os.IsNotExist(err) {
		return nil
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
		return nil
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
	if _, err := file.WriteString(resultsMarker); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(output, ".doe"))
}

func validateManagedPaths(output string) error {
	for _, name := range []string{".doe", "experiments.jsonl", "runs.jsonl"} {
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

func setupOutput(ctx context.Context, stdin io.Reader, root, script, logPath string) (map[string]any, error) {
	last, err := commandOutputToFile(ctx, stdin, root, script, logPath)
	if err != nil {
		return nil, fmt.Errorf("setup (log: %s): %w", logPath, err)
	}
	if last == "" {
		return map[string]any{}, nil
	}
	environment, err := parseObject(last)
	if err != nil {
		return map[string]any{}, nil
	}
	return environment, nil
}

func beginExperiment(ctx context.Context, env *cli.Env, root, output string, s model.Study, execution *studyExecution) error {
	if err := os.MkdirAll(output, 0o755); err != nil {
		return err
	}
	if err := writeMarker(output); err != nil {
		return err
	}
	for _, name := range []string{"experiments.jsonl", "runs.jsonl"} {
		if err := repairJSONL(filepath.Join(output, name)); err != nil {
			return err
		}
	}
	started := time.Now()
	experimentID := rand.Text()
	logDir := filepath.Join(output, experimentID)
	if err := os.Mkdir(logDir, 0o755); err != nil {
		return fmt.Errorf("create experiment log directory: %w", err)
	}
	environment := map[string]any{}
	if s.Setup != "" {
		var err error
		environment, err = setupOutput(ctx, env.Stdin, root, s.Setup, filepath.Join(logDir, "setup.txt"))
		if err != nil {
			return err
		}
	}

	experiment := model.Experiment{
		ID: experimentID, Start: started, Study: s.Name, Designs: execution.designs,
		Factors: s.Designs[0].FactorNames, Files: execution.snapshot.Files, FilesHash: execution.snapshot.Hash,
		Env: environment, EnvHash: objectHash(environment),
	}
	if err := appendJSON(filepath.Join(output, "experiments.jsonl"), experiment); err != nil {
		return err
	}
	execution.results.addExperiment(experiment)
	execution.experiment = experiment
	return nil
}

func conductDesign(ctx context.Context, env *cli.Env, root, output, script string, d model.Design, experiment model.Experiment, results *resultIndex) error {
	points := d.Points
	pointKeys := make([]string, len(points))
	commands := make([]string, len(points))
	for i, point := range points {
		key, err := pointKey(experiment.Study, point)
		if err != nil {
			return err
		}
		pointKeys[i] = key
		command, err := interpolate(script, point)
		if err != nil {
			return err
		}
		commands[i] = command
	}
	schedule := study.Schedule(len(points), d.Replicates)
	reused := 0
	remaining := make([]string, 0, len(points)*d.Replicates)
	for replicate, row := range schedule {
		for _, pointIndex := range row {
			key := replicateKey(pointKeys[pointIndex], replicate+1)
			if _, ok := results.runs[key]; ok {
				reused++
			} else {
				remaining = append(remaining, pointKeys[pointIndex])
			}
		}
	}
	if d.Concurrency > 1 || len(d.ConcurrencyBy) > 0 {
		return conductConcurrent(ctx, env, root, output, d, experiment, results, pointKeys, commands, schedule, reused)
	}
	state := newProgressState(len(points)*d.Replicates, reused, time.Now(), remaining)
	for replicate, row := range schedule {
		for _, pointIndex := range row {
			key := replicateKey(pointKeys[pointIndex], replicate+1)
			if duration, ok := results.runs[key]; ok {
				state.AddDuration(pointKeys[pointIndex], duration)
			}
		}
	}

	var progress *progressBar
	var ticks <-chan time.Time
	var ticker *time.Ticker
	if isTerminal(env.Stderr) {
		progress = newProgress(env.Stderr, experiment.Study+"/"+d.Name)
		progress.Render(state.Snapshot(time.Now()))
		ticker = time.NewTicker(time.Second)
		ticks = ticker.C
		defer func() {
			ticker.Stop()
			progress.Close()
		}()
	}

	for replicate, row := range schedule {
		for _, pointIndex := range row {
			key := replicateKey(pointKeys[pointIndex], replicate+1)
			if _, ok := results.runs[key]; ok {
				continue
			}
			runStart := time.Now()
			state.Start(runStart)
			if progress != nil {
				progress.Render(state.Snapshot(runStart))
			}
			runID := rand.Text()
			logPath := filepath.Join(output, experiment.ID, runID+".txt")
			last, err := commandOutputWithProgress(ctx, env.Stdin, root, commands[pointIndex], logPath, progress, state, ticks)
			if err != nil {
				return fmt.Errorf("replicate %d point #%d (log: %s): %w", replicate+1, pointIndex+1, logPath, err)
			}
			completion := runCompletion{
				task: runTask{pointIndex: pointIndex, replicate: replicate + 1},
				id:   runID, logPath: logPath, start: runStart, end: time.Now(), last: last,
			}
			if err := saveCompletion(output, d, experiment, results, pointKeys, completion); err != nil {
				return err
			}
			state.Complete(completion.end.Sub(completion.start))
			if progress != nil {
				progress.Render(state.Snapshot(completion.end))
			}
		}
	}
	return nil
}

type runTask struct {
	pointIndex int
	replicate  int
}

type runCompletion struct {
	task       runTask
	id         string
	logPath    string
	start, end time.Time
	last       string
	err        error
}

func conductConcurrent(
	ctx context.Context,
	env *cli.Env,
	root, output string,
	d model.Design,
	experiment model.Experiment,
	results *resultIndex,
	pointKeys, commands []string,
	schedule [][]int,
	reused int,
) error {
	groupNames := make([]string, 0)
	groupQueues := make(map[string][]runTask)
	for replicate, row := range schedule {
		for _, pointIndex := range row {
			key := replicateKey(pointKeys[pointIndex], replicate+1)
			if _, ok := results.runs[key]; ok {
				continue
			}
			group := concurrencyGroupKey(d.Points[pointIndex], d.ConcurrencyBy)
			if _, ok := groupQueues[group]; !ok {
				groupNames = append(groupNames, group)
			}
			groupQueues[group] = append(groupQueues[group], runTask{pointIndex: pointIndex, replicate: replicate + 1})
		}
	}
	if len(groupNames) == 0 {
		return nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	completed := make(chan runCompletion)
	sharedEnv := *env
	if env.Stdin != nil {
		if _, ok := env.Stdin.(*os.File); !ok {
			sharedEnv.Stdin = &lockedReader{reader: env.Stdin}
		}
	}
	active := make(map[string]int, len(groupNames))
	next := make(map[string]int, len(groupNames))
	activeTotal := 0
	stopped := false
	var firstErr error

	var progress *progressBar
	if isTerminal(env.Stderr) {
		progress = newProgress(env.Stderr, experiment.Study+"/"+d.Name)
		defer progress.Close()
	}

	dispatch := func() {
		if stopped || runCtx.Err() != nil {
			return
		}
		for _, group := range groupNames {
			queue := groupQueues[group]
			for active[group] < d.Concurrency && next[group] < len(queue) {
				task := queue[next[group]]
				next[group]++
				active[group]++
				activeTotal++
				go func() {
					start := time.Now()
					runID := rand.Text()
					logPath := filepath.Join(output, experiment.ID, runID+".txt")
					last, err := commandOutputToFile(runCtx, sharedEnv.Stdin, root, commands[task.pointIndex], logPath)
					completed <- runCompletion{task: task, id: runID, logPath: logPath, start: start, end: time.Now(), last: last, err: err}
				}()
			}
		}
	}
	dispatch()
	if activeTotal == 0 && runCtx.Err() != nil {
		return runCtx.Err()
	}
	if progress != nil {
		progress.Render(progressSnapshot{total: len(d.Points) * d.Replicates, done: reused, status: fmt.Sprintf("%d active", activeTotal)})
	}
	done := reused
	ctxDone := ctx.Done()
	for activeTotal > 0 {
		select {
		case completion := <-completed:
			group := concurrencyGroupKey(d.Points[completion.task.pointIndex], d.ConcurrencyBy)
			active[group]--
			activeTotal--
			if completion.err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("replicate %d point #%d (log: %s): %w", completion.task.replicate, completion.task.pointIndex+1, completion.logPath, completion.err)
				}
				stopped = true
				cancel()
			} else if err := saveCompletion(output, d, experiment, results, pointKeys, completion); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				stopped = true
				cancel()
			} else {
				done++
			}
			dispatch()
			if progress != nil {
				status := fmt.Sprintf("%d active", activeTotal)
				if done == len(d.Points)*d.Replicates {
					status = "done"
				}
				progress.Render(progressSnapshot{total: len(d.Points) * d.Replicates, done: done, status: status})
			}
		case <-ctxDone:
			if firstErr == nil {
				firstErr = ctx.Err()
			}
			stopped = true
			cancel()
			ctxDone = nil
		}
	}
	if firstErr == nil && runCtx.Err() != nil {
		return runCtx.Err()
	}
	return firstErr
}

func saveCompletion(output string, d model.Design, experiment model.Experiment, results *resultIndex, pointKeys []string, completion runCompletion) error {
	if err := saveCompletionResult(output, d, experiment, results, pointKeys, completion); err != nil {
		return fmt.Errorf("%w (log: %s)", err, completion.logPath)
	}
	return nil
}

func saveCompletionResult(output string, d model.Design, experiment model.Experiment, results *resultIndex, pointKeys []string, completion runCompletion) error {
	if completion.last == "" {
		return fmt.Errorf("replicate %d point #%d: run produced no result", completion.task.replicate, completion.task.pointIndex+1)
	}
	outputs, err := parseObject(completion.last)
	if err != nil {
		return fmt.Errorf("replicate %d point #%d result: %w", completion.task.replicate, completion.task.pointIndex+1, err)
	}
	inputs := pointMap(d.Points[completion.task.pointIndex])
	for name := range outputs {
		if model.IsReservedRunField(name) {
			return fmt.Errorf("response name %q is reserved", name)
		}
		if _, exists := inputs[name]; exists {
			return fmt.Errorf("response name %q is also a factor", name)
		}
	}
	run := model.Run{
		ID: completion.id, ExperimentID: experiment.ID, Replicate: completion.task.replicate,
		Start: completion.start, End: completion.end, Inputs: inputs, Outputs: outputs,
	}
	if err := appendJSON(filepath.Join(output, "runs.jsonl"), flattenRun(run)); err != nil {
		return err
	}
	key := replicateKey(pointKeys[completion.task.pointIndex], completion.task.replicate)
	results.runs[key] = run.End.Sub(run.Start)
	return nil
}

func concurrencyGroupKey(point model.Point, names []string) string {
	if len(names) == 0 {
		return ""
	}
	values := pointMap(point)
	selected := make([]model.Scalar, len(names))
	for i, name := range names {
		selected[i] = values[name]
	}
	encoded, _ := json.Marshal(selected)
	return string(encoded)
}

type lockedReader struct {
	mu     sync.Mutex
	reader io.Reader
}

func (r *lockedReader) Read(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reader.Read(data)
}

func isTerminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

type commandResult struct {
	last string
	err  error
}

func commandOutputWithProgress(
	ctx context.Context,
	stdin io.Reader,
	root, script, logPath string,
	progress *progressBar,
	state *progressState,
	ticks <-chan time.Time,
) (string, error) {
	result := make(chan commandResult, 1)
	go func() {
		last, err := commandOutputToFile(ctx, stdin, root, script, logPath)
		result <- commandResult{last: last, err: err}
	}()
	for {
		select {
		case completed := <-result:
			return completed.last, completed.err
		case now := <-ticks:
			progress.Render(state.Snapshot(now))
		}
	}
}

func commandOutputToFile(ctx context.Context, stdin io.Reader, root, script, path string) (last string, err error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	last, commandErr := commandOutput(ctx, stdin, root, script, file)
	if closeErr := file.Close(); commandErr == nil {
		commandErr = closeErr
	}
	return last, commandErr
}

func commandOutput(ctx context.Context, stdin io.Reader, root, script string, log io.Writer) (string, error) {
	command := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	// Bound pipe waits if cancellation races with a child process starting.
	command.WaitDelay = time.Second
	command.Dir = root
	command.Stdin = stdin
	output := &commandLog{log: log}
	reader, writer := io.Pipe()
	command.Stdout = stdoutLog{output: output, writer: writer}
	command.Stderr = output
	configureProcessGroup(command)

	parsed := make(chan commandResult, 1)
	go func() {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		last := ""
		for scanner.Scan() {
			last = scanner.Text()
		}
		err := scanner.Err()
		_ = reader.CloseWithError(err)
		if err != nil {
			_ = command.Cancel()
		}
		parsed <- commandResult{last: last, err: err}
	}()

	runErr := command.Run()
	_ = writer.Close()
	result := <-parsed
	if result.err != nil {
		return "", result.err
	}
	if runErr != nil {
		return "", runErr
	}
	return result.last, nil
}

type commandLog struct {
	mu  sync.Mutex
	log io.Writer
}

func (w *commandLog) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.log.Write(data)
}

type stdoutLog struct {
	output *commandLog
	writer *io.PipeWriter
}

func (w stdoutLog) Write(data []byte) (int, error) {
	w.output.mu.Lock()
	defer w.output.mu.Unlock()
	n, err := w.output.log.Write(data)
	if err != nil {
		return n, err
	}
	if n != len(data) {
		return n, io.ErrShortWrite
	}
	return w.writer.Write(data)
}

func parseObject(line string) (map[string]any, error) {
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

func objectHash(object map[string]any) string {
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
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	data := encoded.Bytes()
	if info.Size() > 0 {
		var last [1]byte
		if _, err := file.ReadAt(last[:], info.Size()-1); err != nil {
			_ = file.Close()
			return err
		}
		if last[0] != '\n' {
			data = append([]byte{'\n'}, data...)
		}
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
