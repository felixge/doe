package model

// Schedule returns one row of point indexes per replicate. Rows follow a
// Williams design: a full cycle balances execution position and first-order
// carryover, using n rows for even n and 2n rows for odd n greater than one.
// Additional replicates repeat the cycle.
func Schedule(pointCount, replicates int) [][]int {
	if pointCount <= 0 || replicates <= 0 {
		return nil
	}

	// Alternate from either end of the point list to balance adjacent pairs.
	base := make([]int, pointCount)
	for position := range base {
		switch {
		case position == 0:
			base[position] = 0
		case position%2 == 1:
			base[position] = (position + 1) / 2
		default:
			base[position] = pointCount - position/2
		}
	}

	// Odd-sized designs need a reversed pass to balance carryover direction.
	period := pointCount
	if pointCount > 1 && pointCount%2 == 1 {
		period *= 2
	}
	rows := make([][]int, replicates)
	for replicate := range rows {
		cycleRow := replicate % period
		shift := cycleRow % pointCount
		row := make([]int, pointCount)
		for position := range row {
			basePosition := position
			if cycleRow >= pointCount {
				basePosition = pointCount - 1 - position
			}
			row[position] = (base[basePosition] + shift) % pointCount
		}
		rows[replicate] = row
	}
	return rows
}
