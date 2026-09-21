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
	for designIndex, d := range study.Designs {
		if len(study.Designs) > 1 {
			if designIndex > 0 {
				_, _ = fmt.Fprintln(w)
			}
			_, _ = fmt.Fprintln(w, d.Path)
		}
		points := d.Points
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
		writeTable(w, pointRows)
		_, _ = fmt.Fprintln(w)

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
		writeTable(w, scheduleRows)
	}
	return nil
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

func writeTable(w io.Writer, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for column, cell := range row {
			if len(cell) > widths[column] {
				widths[column] = len(cell)
			}
		}
	}
	border := func() {
		_, _ = fmt.Fprint(w, "+")
		for _, width := range widths {
			_, _ = fmt.Fprint(w, "-"+strings.Repeat("-", width)+"-+")
		}
		_, _ = fmt.Fprintln(w)
	}
	border()
	for rowIndex, row := range rows {
		_, _ = fmt.Fprint(w, "|")
		for column, cell := range row {
			_, _ = fmt.Fprintf(w, " %-*s |", widths[column], cell)
		}
		_, _ = fmt.Fprintln(w)
		if rowIndex == 0 {
			border()
		}
	}
	border()
}
