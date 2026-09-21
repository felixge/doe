package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/felixge/doe/internal/model"
)

type resultIndex struct {
	experiments []model.Experiment
	experiment  map[string]model.Experiment
	runs        map[string]time.Duration
}

func loadResults(output string) (*resultIndex, error) {
	index := &resultIndex{
		experiment: make(map[string]model.Experiment),
		runs:       make(map[string]time.Duration),
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
		start, err := runTime(record, "start")
		if err != nil {
			return err
		}
		end, err := runTime(record, "end")
		if err != nil {
			return err
		}
		if end.Before(start) {
			return fmt.Errorf("run ends before it starts")
		}
		index.runs[key] = end.Sub(start)
		return nil
	}); err != nil {
		return nil, err
	}
	return index, nil
}

func runTime(record map[string]any, name string) (time.Time, error) {
	text, ok := record[name].(string)
	if !ok {
		return time.Time{}, fmt.Errorf("run has invalid %s", name)
	}
	value, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}, fmt.Errorf("run has invalid %s", name)
	}
	return value, nil
}

func (r *resultIndex) addExperiment(experiment model.Experiment) {
	r.experiments = append(r.experiments, experiment)
	r.experiment[experiment.ID] = experiment
}

func reuseKey(design string, replicate int, point model.Point) (string, error) {
	key, err := pointKey(design, point)
	if err != nil {
		return "", err
	}
	return replicateKey(key, replicate), nil
}

func replicateKey(pointKey string, replicate int) string {
	return pointKey + "\x00" + strconv.Itoa(replicate)
}

func pointKey(design string, point model.Point) (string, error) {
	values := make(map[string]model.Scalar, len(point.Values))
	for _, value := range point.Values {
		values[value.Name] = value.Value
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return design + "\x00" + string(data), nil
}

func readJSONL(path string, consume func([]byte) error) (err error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()

	reader := bufio.NewReader(file)
	var offset int64
	for line := 1; ; line++ {
		data, readErr := reader.ReadBytes('\n')
		if len(data) == 0 && errors.Is(readErr, io.EOF) {
			return nil
		}
		start := offset
		offset += int64(len(data))
		terminated := len(data) > 0 && data[len(data)-1] == '\n'
		if terminated {
			data = data[:len(data)-1]
			if len(data) > 0 && data[len(data)-1] == '\r' {
				data = data[:len(data)-1]
			}
		}
		if len(bytes.TrimSpace(data)) != 0 {
			decoder := json.NewDecoder(bytes.NewReader(data))
			var value json.RawMessage
			if decodeErr := decoder.Decode(&value); decodeErr != nil {
				if !terminated && errors.Is(readErr, io.EOF) {
					if err := file.Truncate(start); err != nil {
						return err
					}
					return file.Sync()
				}
				return fmt.Errorf("%s:%d: %w", path, line, decodeErr)
			}
			if decodeErr := ensureEOF(decoder); decodeErr != nil {
				return fmt.Errorf("%s:%d: %w", path, line, decodeErr)
			}
			if consumeErr := consume(data); consumeErr != nil {
				return fmt.Errorf("%s:%d: %w", path, line, consumeErr)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}
