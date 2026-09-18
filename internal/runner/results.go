package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/felixge/doe/internal/model"
)

type resultIndex struct {
	root        string
	experiments []model.Experiment
	experiment  map[string]model.Experiment
	runs        map[string]bool
}

func loadResults(output, root string) (*resultIndex, error) {
	index := &resultIndex{
		root:       root,
		experiment: make(map[string]model.Experiment),
		runs:       make(map[string]bool),
	}
	if err := readJSONL(filepath.Join(output, "experiments.jsonl"), func(data []byte) error {
		var experiment model.Experiment
		if err := json.Unmarshal(data, &experiment); err != nil {
			return err
		}
		if experiment.ID == "" {
			return fmt.Errorf("experiment has no experiment_id")
		}
		index.addExperiment(experiment)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := readJSONL(filepath.Join(output, "runs.jsonl"), func(data []byte) error {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			return err
		}
		experimentID, ok := record["experiment_id"].(string)
		if !ok {
			return fmt.Errorf("run has no experiment_id")
		}
		experiment, ok := index.experiment[experimentID]
		if !ok {
			return fmt.Errorf("run references unknown experiment %q", experimentID)
		}
		replicateNumber, ok := record["replicate"].(json.Number)
		if !ok {
			return fmt.Errorf("run has invalid replicate")
		}
		replicate, err := strconv.Atoi(replicateNumber.String())
		if err != nil || replicate < 1 {
			return fmt.Errorf("run has invalid replicate")
		}
		point := model.Point{Values: make([]model.Value, 0, len(experiment.Factors))}
		for _, name := range experiment.Factors {
			value, ok := record[name]
			if !ok {
				return fmt.Errorf("run is missing factor %q", name)
			}
			point.Values = append(point.Values, model.Value{Name: name, Value: value})
		}
		key, err := reuseKey(experiment.Design, replicate, point)
		if err != nil {
			return err
		}
		index.runs[key] = true
		return nil
	}); err != nil {
		return nil, err
	}
	return index, nil
}

func (r *resultIndex) addExperiment(experiment model.Experiment) {
	r.experiments = append(r.experiments, experiment)
	r.experiment[experiment.ID] = experiment
}

func reuseKey(design string, replicate int, point model.Point) (string, error) {
	values := make(map[string]model.Scalar, len(point.Values))
	for _, value := range point.Values {
		values[value.Name] = value.Value
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return design + "\x00" + strconv.Itoa(replicate) + "\x00" + string(data), nil
}

func readJSONL(path string, consume func([]byte) error) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if len(scanner.Bytes()) == 0 {
			continue
		}
		if err := consume(scanner.Bytes()); err != nil {
			return fmt.Errorf("%s:%d: %w", path, line, err)
		}
	}
	return scanner.Err()
}
