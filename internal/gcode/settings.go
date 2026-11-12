package gcode

import (
	"strconv"
	"strings"
)

// Settings holds the slicer config block embedded in the gcode ("; key = value").
type Settings map[string]string

// Float returns the idx-th comma separated value of key as a number.
// Percent signs are stripped (so "15%" becomes 15). ok is false when missing.
func (s Settings) Float(key string, idx int) (float64, bool) {
	v, found := s[key]
	if !found {
		return 0, false
	}
	parts := strings.Split(v, ",")
	if idx >= len(parts) {
		idx = len(parts) - 1
	}
	if idx < 0 {
		idx = 0
	}
	p := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(parts[idx]), "%"))
	f, err := strconv.ParseFloat(p, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// FloatOr is Float with a default.
func (s Settings) FloatOr(key string, idx int, def float64) float64 {
	if f, ok := s.Float(key, idx); ok {
		return f
	}
	return def
}

// featureSpeedKey maps a gcode feature label to the config key holding its normal speed.
// Only features the slicer's cooling slowdown is allowed to stretch are listed.
var featureSpeedKey = map[string]string{
	"Outer wall":              "outer_wall_speed",
	"Inner wall":              "inner_wall_speed",
	"Sparse infill":           "sparse_infill_speed",
	"Internal solid infill":   "internal_solid_infill_speed",
	"Floating vertical shell": "internal_solid_infill_speed",
	"Top surface":             "top_surface_speed",
	"Gap infill":              "gap_infill_speed",
	"Support":                 "support_speed",
	"Support interface":       "support_interface_speed",
	// PrusaSlicer / Orca style labels
	"Perimeter":          "inner_wall_speed",
	"External perimeter": "outer_wall_speed",
	"Internal infill":    "sparse_infill_speed",
	"Solid infill":       "internal_solid_infill_speed",
	"Top solid infill":   "top_surface_speed",
	"Support material":   "support_speed",
}

// referenceFeatures are the features used to measure how much a layer was slowed.
var referenceFeatures = map[string]bool{
	"Inner wall":            true,
	"Sparse infill":         true,
	"Internal solid infill": true,
	"Perimeter":             true,
	"Internal infill":       true,
	"Solid infill":          true,
}
