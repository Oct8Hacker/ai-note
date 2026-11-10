package frames

import "math"

// ChooseTime snaps half a second past the nearest eligible scene cut.
func ChooseTime(requested float64, changes []float64, maxDrift float64) float64 {
	best := requested
	nearest := math.Inf(1)
	limit := min(2, maxDrift)
	for _, cut := range changes {
		candidate := cut + 0.5
		distance := math.Abs(cut - requested)
		if candidate >= 0 && math.Abs(candidate-requested) <= limit && distance < nearest {
			best = candidate
			nearest = distance
		}
	}
	return best
}

// ClampDrift keeps a capture time within the drift allowance and the video timeline.
func ClampDrift(requested, t, maxDrift float64) float64 {
	drift := max(0, maxDrift)
	return max(0, requested-drift, min(t, requested+drift))
}
