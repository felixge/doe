package model

import (
	"reflect"
	"strconv"
	"testing"
)

func TestSchedule(t *testing.T) {
	for _, tc := range []struct {
		name       string
		points     int
		replicates int
		want       [][]int
	}{
		{"even", 4, 6, [][]int{
			{0, 1, 3, 2}, {1, 2, 0, 3}, {2, 3, 1, 0}, {3, 0, 2, 1},
			{0, 1, 3, 2}, {1, 2, 0, 3},
		}},
		{"odd", 3, 8, [][]int{
			{0, 1, 2}, {1, 2, 0}, {2, 0, 1},
			{2, 1, 0}, {0, 2, 1}, {1, 0, 2},
			{0, 1, 2}, {1, 2, 0},
		}},
		{"single point", 1, 3, [][]int{{0}, {0}, {0}}},
		{"no points", 0, 2, nil},
		{"no replicates", 2, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Schedule(tc.points, tc.replicates); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Schedule(%d, %d) = %v, want %v", tc.points, tc.replicates, got, tc.want)
			}
		})
	}
}

func TestScheduleBalance(t *testing.T) {
	for n := 2; n <= 9; n++ {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			period := n
			if n%2 == 1 {
				period *= 2
			}
			position := make([][]int, n)
			carryover := make([][]int, n)
			for point := range position {
				position[point] = make([]int, n)
				carryover[point] = make([]int, n)
			}
			for _, row := range Schedule(n, period) {
				seen := make([]bool, n)
				for index, point := range row {
					if seen[point] {
						t.Fatalf("duplicate point in row %v", row)
					}
					seen[point] = true
					position[point][index]++
					if index > 0 {
						carryover[row[index-1]][point]++
					}
				}
			}
			for point := 1; point < n; point++ {
				if !reflect.DeepEqual(position[point], position[0]) {
					t.Errorf("unbalanced positions: %v", position)
					break
				}
			}
			want := carryover[0][1]
			for previous := range n {
				for next := range n {
					if next != previous && carryover[previous][next] != want {
						t.Fatalf("unbalanced carryover: %v", carryover)
					}
				}
			}
		})
	}
}

func TestScheduleRepeatedRowsIndependent(t *testing.T) {
	rows := Schedule(3, 7)
	rows[0][0] = -1
	if rows[6][0] != 0 {
		t.Errorf("changing a row changed its repetition: %v", rows[6])
	}
}

func TestScheduleLargeSingleReplicate(t *testing.T) {
	const points = 100_000
	rows := Schedule(points, 1)
	if len(rows) != 1 || len(rows[0]) != points {
		t.Fatalf("Schedule(%d, 1) has dimensions %d x %d", points, len(rows), len(rows[0]))
	}
	if want := []int{0, 1, points - 1, 2, points - 2, 3}; !reflect.DeepEqual(rows[0][:6], want) {
		t.Errorf("first points = %v, want %v", rows[0][:6], want)
	}
}
