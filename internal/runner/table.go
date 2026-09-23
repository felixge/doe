package runner

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/felixge/doe/internal/model"
	"github.com/felixge/doe/internal/study"
)

func planDesigns(w io.Writer, selected []study.Selection, executions map[string]*studyExecution) error {
	totalRuns, totalReused := 0, 0
	for designIndex, selection := range selected {
		d := *selection.Design
		if designIndex > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w, selection); err != nil {
			return err
		}
		points := d.Points
		totalRuns += len(points) * d.Replicates
		if _, err := fmt.Fprintln(w, "Design points:"); err != nil {
			return err
		}
		pointRows := make([][]string, 0, len(points)+1)
		header := append([]string{"point"}, d.FactorNames...)
		pointRows = append(pointRows, header)
		for index, point := range points {
			row := []string{"#" + strconv.Itoa(index+1)}
			for _, value := range point.Values {
				row = append(row, scalarText(value.Value))
			}
			pointRows = append(pointRows, row)
		}
		if err := writeTable(w, pointRows); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}

		groups := planGroups(d)
		limit := max(d.Concurrency, 1)
		if d.Concurrency > 1 || len(d.ConcurrencyBy) > 0 {
			if _, err := fmt.Fprintf(w, "Concurrency: %d per group, %d groups, %d maximum active runs\n\n", limit, len(groups), limit*len(groups)); err != nil {
				return err
			}
		}
		schedule := study.Schedule(len(points), d.Replicates)
		reused := map[string]bool{}
		results := executions[selection.Study.Name].results
		for replicate, row := range schedule {
			for _, pointIndex := range row {
				key, err := reuseKey(selection.Study.Name, d.Name, replicate+1, points[pointIndex])
				if err != nil {
					return err
				}
				if _, ok := results.runs[key]; ok {
					reused[key] = true
				}
			}
		}
		totalReused += len(reused)
		for groupIndex, group := range groups {
			if groupIndex > 0 {
				if _, err := fmt.Fprintln(w); err != nil {
					return err
				}
			}
			label := "Schedule:"
			if len(d.ConcurrencyBy) > 0 {
				settings := make([]string, len(d.ConcurrencyBy))
				values := pointMap(points[group[0]])
				for i, name := range d.ConcurrencyBy {
					settings[i] = name + "=" + scalarText(values[name])
				}
				label = "Schedule (" + strings.Join(settings, ", ") + "):"
			}
			if _, err := fmt.Fprintln(w, label); err != nil {
				return err
			}
			included := make(map[int]bool, len(group))
			for _, pointIndex := range group {
				included[pointIndex] = true
			}
			filteredSchedule := make([][]int, len(schedule))
			for replicate, row := range schedule {
				for _, pointIndex := range row {
					if included[pointIndex] {
						filteredSchedule[replicate] = append(filteredSchedule[replicate], pointIndex)
					}
				}
			}
			scheduleRows := make([][]string, 0, len(group)+1)
			header = []string{"run/rep"}
			for replicate := range schedule {
				header = append(header, strconv.Itoa(replicate+1))
			}
			scheduleRows = append(scheduleRows, header)
			for position := range group {
				rowValues := []string{strconv.Itoa(position + 1)}
				for replicate, row := range filteredSchedule {
					cell := "#" + strconv.Itoa(row[position]+1)
					key, err := reuseKey(selection.Study.Name, d.Name, replicate+1, points[row[position]])
					if err != nil {
						return err
					}
					if reused[key] {
						cell += "*"
					}
					rowValues = append(rowValues, cell)
				}
				scheduleRows = append(scheduleRows, rowValues)
			}
			if err := writeTable(w, scheduleRows); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "\nRuns: %d; reusable: %d; new: %d\n", len(points)*d.Replicates, len(reused), len(points)*d.Replicates-len(reused)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "\nTotal runs: %d; reusable: %d; new: %d\n* Reusable from existing results for the same design.\n", totalRuns, totalReused, totalRuns-totalReused)
	return err
}

func planGroups(d model.Design) [][]int {
	groups := make([][]int, 0)
	indexes := make(map[string]int)
	for pointIndex, point := range d.Points {
		key := concurrencyGroupKey(point, d.ConcurrencyBy)
		groupIndex, ok := indexes[key]
		if !ok {
			groupIndex = len(groups)
			indexes[key] = groupIndex
			groups = append(groups, nil)
		}
		groups[groupIndex] = append(groups[groupIndex], pointIndex)
	}
	return groups
}

func scalarText(value model.Scalar) string {
	if value == nil {
		return "null"
	}
	if value, ok := value.(string); ok {
		return value
	}
	return fmt.Sprint(value)
}

func writeTable(w io.Writer, rows [][]string) error {
	if len(rows) == 0 {
		return nil
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for column, cell := range row {
			if len(cell) > widths[column] {
				widths[column] = len(cell)
			}
		}
	}
	border := func() error {
		if _, err := fmt.Fprint(w, "+"); err != nil {
			return err
		}
		for _, width := range widths {
			if _, err := fmt.Fprint(w, "-"+strings.Repeat("-", width)+"-+"); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	if err := border(); err != nil {
		return err
	}
	for rowIndex, row := range rows {
		if _, err := fmt.Fprint(w, "|"); err != nil {
			return err
		}
		for column, cell := range row {
			if _, err := fmt.Fprintf(w, " %-*s |", widths[column], cell); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		if rowIndex == 0 {
			if err := border(); err != nil {
				return err
			}
		}
	}
	return border()
}
