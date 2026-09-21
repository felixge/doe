package runner

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/felixge/doe/internal/design"
	"github.com/felixge/doe/internal/model"
)

func planStudy(w io.Writer, study model.Study) error {
	totalRuns := 0
	for designIndex, d := range study.Designs {
		if len(study.Designs) > 1 {
			if designIndex > 0 {
				if _, err := fmt.Fprintln(w); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(w, d.Path); err != nil {
				return err
			}
		}
		points := d.Points
		totalRuns += len(points) * d.Replicates
		if _, err := fmt.Fprintln(w, "Design points:"); err != nil {
			return err
		}
		pointRows := make([][]string, 0, len(points)+1)
		header := append([]string{"#"}, d.FactorNames...)
		pointRows = append(pointRows, header)
		for index, point := range points {
			row := []string{strconv.Itoa(index + 1)}
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

		if _, err := fmt.Fprintln(w, "Schedule:"); err != nil {
			return err
		}
		schedule := design.Schedule(len(points), d.Replicates)
		scheduleRows := make([][]string, 0, len(schedule)+1)
		header = []string{"replicate"}
		for position := range points {
			header = append(header, strconv.Itoa(position+1))
		}
		scheduleRows = append(scheduleRows, header)
		for replicate, row := range schedule {
			values := []string{strconv.Itoa(replicate + 1)}
			for _, point := range row {
				values = append(values, strconv.Itoa(point+1))
			}
			scheduleRows = append(scheduleRows, values)
		}
		if err := writeTable(w, scheduleRows); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "\nTotal runs: %d\n", totalRuns)
	return err
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
