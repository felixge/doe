package model

import "time"

type durationStats struct {
	count int
	total time.Duration
}

func (s *durationStats) add(duration time.Duration) {
	s.count++
	s.total += duration
}

func (s durationStats) average() (time.Duration, bool) {
	if s.count == 0 {
		return 0, false
	}
	return s.total / time.Duration(s.count), true
}

// EstimateTotalDuration estimates the duration of the remaining sequential runs.
// Each value in remaining is an index into the experiment's Points slice, in
// execution order. The keys in samples are the same point indexes; their values
// are completed run durations. Unseen points use the global average. Elapsed
// time is subtracted from the first remaining run.
func EstimateTotalDuration(remaining []int, samples map[int][]time.Duration, elapsed time.Duration) (time.Duration, bool) {
	points := make(map[int]durationStats, len(samples))
	var all durationStats
	for point, durations := range samples {
		var stats durationStats
		for _, duration := range durations {
			stats.add(duration)
			all.add(duration)
		}
		points[point] = stats
	}

	var total time.Duration
	for index, point := range remaining {
		duration, ok := points[point].average()
		if !ok {
			duration, ok = all.average()
		}
		if !ok {
			return 0, false
		}
		if index == 0 {
			duration = max(duration-max(elapsed, 0), 0)
		}
		total += duration
	}
	return total, true
}
