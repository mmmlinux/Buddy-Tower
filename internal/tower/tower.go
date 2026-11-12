// Package tower designs a round "buddy" tower whose diameter changes layer by layer
// so that every layer of the print takes at least a minimum amount of time.
package tower

import (
	"math"
	"strings"

	"buddy-tower/internal/gcode"
)

// PrintModel describes how the slicer will print the tower, taken from the 3MF settings.
type PrintModel struct {
	Walls                   int
	OuterW, InnerW, InfillW float64 // line widths (mm)
	Density                 float64 // sparse infill density 0..1
	VOuter, VInner, VInfill float64 // mm/s
	AOuter, AInner, AInfill float64 // mm/s²
	MaxVolumetric           float64 // mm³/s, 0 = unlimited
	SlowDownLayerTime       float64 // slicer's own cooling threshold (s)
}

// ModelFromSettings builds a PrintModel from the gcode's embedded config.
func ModelFromSettings(s gcode.Settings) PrintModel {
	def := s.FloatOr("default_acceleration", 0, 5000)
	accel := func(key string) float64 {
		raw := strings.TrimSpace(strings.Split(s[key], ",")[0])
		v, ok := s.Float(key, 0)
		if !ok || v <= 0 {
			return def
		}
		if strings.HasSuffix(raw, "%") {
			return def * v / 100
		}
		return v
	}
	lw := s.FloatOr("line_width", 0, 0.42)
	width := func(key string) float64 {
		v := s.FloatOr(key, 0, 0)
		if v <= 0 {
			return lw
		}
		return v
	}
	m := PrintModel{
		Walls:             int(s.FloatOr("wall_loops", 0, 2)),
		OuterW:            width("outer_wall_line_width"),
		InnerW:            width("inner_wall_line_width"),
		InfillW:           width("sparse_infill_line_width"),
		Density:           s.FloatOr("sparse_infill_density", 0, 15) / 100,
		VOuter:            s.FloatOr("outer_wall_speed", 0, 60),
		VInner:            s.FloatOr("inner_wall_speed", 0, 150),
		VInfill:           s.FloatOr("sparse_infill_speed", 0, 200),
		AOuter:            accel("outer_wall_acceleration"),
		AInner:            accel("inner_wall_acceleration"),
		AInfill:           accel("sparse_infill_acceleration"),
		MaxVolumetric:     s.FloatOr("filament_max_volumetric_speed", 0, 0),
		SlowDownLayerTime: s.FloatOr("slow_down_layer_time", 0, 0),
	}
	if m.Walls < 1 {
		m.Walls = 1
	}
	return m
}

func (m PrintModel) speed(v, w, h float64) float64 {
	if m.MaxVolumetric > 0 && w > 0 && h > 0 {
		v = math.Min(v, m.MaxVolumetric/(w*h))
	}
	return v
}

// segTime is the time to print a straight run of length d starting and ending slow.
func segTime(d, v, a float64) float64 {
	if d <= 0 || v <= 0 {
		return 0
	}
	if a <= 0 {
		return d / v
	}
	if d >= v*v/a {
		return d/v + v/a
	}
	return 2 * math.Sqrt(d/a)
}

// LayerTime estimates seconds to print one tower layer of radius r and height h.
func (m PrintModel) LayerTime(r, h float64) float64 {
	if r <= 0 {
		return 0
	}
	t := 0.0
	// perimeters
	rc := r - m.OuterW/2
	if rc <= 0 {
		return 0
	}
	t += segTime(2*math.Pi*rc, m.speed(m.VOuter, m.OuterW, h), m.AOuter)
	inner := r - m.OuterW
	for i := 1; i < m.Walls; i++ {
		rc := inner - m.InnerW*(float64(i)-0.5)
		if rc <= m.InnerW/2 {
			break
		}
		t += segTime(2*math.Pi*rc, m.speed(m.VInner, m.InnerW, h), m.AInner)
	}
	// sparse infill inside the walls
	ri := inner - m.InnerW*float64(m.Walls-1)
	if ri > 0 && m.Density > 0 {
		length := math.Pi * ri * ri * m.Density / m.InfillW
		chord := math.Pi * ri / 2 // average chord across a circle
		n := math.Max(1, length/chord)
		t += n * segTime(length/n, m.speed(m.VInfill, m.InfillW, h), m.AInfill)
	}
	return t
}

// RadiusFor returns the smallest radius whose layer time reaches need, capped at rMax.
// ok is false when even rMax is not enough.
func (m PrintModel) RadiusFor(need, h, rMax float64) (float64, bool) {
	if need <= 0 {
		return 0, true
	}
	if m.LayerTime(rMax, h) < need {
		return rMax, false
	}
	lo, hi := 0.0, rMax
	for i := 0; i < 50; i++ {
		mid := (lo + hi) / 2
		if m.LayerTime(mid, h) >= need {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi, true
}

// Params are the user-tunable design rules.
type Params struct {
	TargetTime   float64 // minimum layer time to reach (s)
	MarginPct    float64 // extra safety margin on the needed time (%)
	Overhead     float64 // seconds per layer the slicer spends travelling to/from the tower
	MinDiameter  float64 // never thinner than this (mm)
	MaxDiameter  float64 // never wider than this (mm)
	OverhangDeg  float64 // steepest widening angle, measured from vertical
	ShrinkDeg    float64 // steepest narrowing angle, measured from vertical
	FootDiameter float64 // wide base for bed grip (0 = none)
	FootHeight   float64 // height of the straight part of the foot
	Tolerance    float64 // how far (outward only) the simplified shape may stray (mm)
	UseNominal   bool    // size from slowdown-removed layer times
}

// DefaultParams mirror the sample tower: Ø10 shaft, 45° growth, 60° narrowing, Ø30 foot.
func DefaultParams(target float64) Params {
	return Params{
		TargetTime:   target,
		MarginPct:    10,
		Overhead:     0,
		MinDiameter:  10,
		MaxDiameter:  60,
		OverhangDeg:  45,
		ShrinkDeg:    60,
		FootDiameter: 30,
		FootHeight:   0.2,
		Tolerance:    0.1,
		UseNominal:   true,
	}
}

// Point is one vertex of the revolved profile: radius r at height z.
type Point struct{ Z, R float64 }

// LayerInfo is the per-layer outcome of a design.
type LayerInfo struct {
	PartTime  float64 // time of the part's layer used for sizing
	Need      float64 // tower time needed
	NeedR     float64 // radius needed to satisfy the layer on its own
	R         float64 // radius actually used (after shape rules), at the layer's middle
	TowerTime float64 // estimated tower time at R
	Short     bool    // target cannot be met even at max diameter
}

// Design is the finished tower.
type Design struct {
	Profile    []Point // simplified profile, bottom to top
	Layers     []LayerInfo
	Height     float64
	MaxR       float64
	NeedLayers int // layers that are under target without a tower
	ShortCount int // layers still under target at max diameter
	Volume     float64
}

// Build designs a tower for the given layers.
func Build(layers []gcode.Layer, m PrintModel, p Params) Design {
	n := len(layers)
	var d Design
	if n == 0 {
		return d
	}
	target := p.TargetTime * (1 + p.MarginPct/100)
	rMax := p.MaxDiameter / 2
	rMin := p.MinDiameter / 2
	if rMin > rMax {
		rMin = rMax
	}
	d.Layers = make([]LayerInfo, n)
	last := -1
	for i, l := range layers {
		pt := l.Time
		if p.UseNominal {
			pt = l.NominalTime
		}
		need := target - pt - p.Overhead
		info := LayerInfo{PartTime: pt, Need: math.Max(0, need)}
		if pt < p.TargetTime {
			d.NeedLayers++
		}
		if need > 0 {
			last = i
			r, ok := m.RadiusFor(need, l.Height, rMax)
			info.NeedR = r
			info.Short = !ok
		}
		d.Layers[i] = info
	}
	if last < 0 {
		return d
	}
	top := last + 1 // number of layers the tower spans

	// boundary heights b[0..top]; layer i spans b[i]..b[i+1]
	b := make([]float64, top+1)
	b[0] = math.Max(0, layers[0].Z-layers[0].Height)
	for i := 0; i < top; i++ {
		b[i+1] = layers[i].Z
	}
	rho := make([]float64, top+1)
	for j := 0; j <= top; j++ {
		r := rMin
		if j > 0 {
			r = math.Max(r, d.Layers[j-1].NeedR)
		}
		if j < top {
			r = math.Max(r, d.Layers[j].NeedR)
		}
		if p.FootDiameter > 0 && b[j] <= p.FootHeight+1e-6 {
			r = math.Max(r, p.FootDiameter/2)
		}
		rho[j] = math.Min(r, math.Max(rMax, p.FootDiameter/2))
	}
	tanO := math.Tan(clampDeg(p.OverhangDeg) * math.Pi / 180)
	tanS := math.Tan(clampDeg(p.ShrinkDeg) * math.Pi / 180)
	// widening going up must start early enough to respect the overhang angle
	for j := top - 1; j >= 0; j-- {
		dz := b[j+1] - b[j]
		rho[j] = math.Max(rho[j], rho[j+1]-dz*tanO)
	}
	// narrowing going up is limited by the shrink angle
	for j := 0; j < top; j++ {
		dz := b[j+1] - b[j]
		rho[j+1] = math.Max(rho[j+1], rho[j]-dz*tanS)
	}
	for i := 0; i < n; i++ {
		info := &d.Layers[i]
		if i < top {
			info.R = (rho[i] + rho[i+1]) / 2
			info.TowerTime = m.LayerTime(math.Min(rho[i], rho[i+1]), layers[i].Height)
			info.Short = info.PartTime+info.TowerTime+p.Overhead < target-1e-6 && info.Need > 0
		}
		if info.Short {
			d.ShortCount++
		}
	}
	d.Profile = simplify(b, rho, p.Tolerance)
	d.Height = b[top]
	for _, pt := range d.Profile {
		d.MaxR = math.Max(d.MaxR, pt.R)
	}
	for k := 1; k < len(d.Profile); k++ {
		a, c := d.Profile[k-1], d.Profile[k]
		h := c.Z - a.Z
		d.Volume += math.Pi * h / 3 * (a.R*a.R + a.R*c.R + c.R*c.R)
	}
	return d
}

func clampDeg(v float64) float64 {
	return math.Max(1, math.Min(85, v))
}

// simplify reduces the per-layer profile to as few straight runs as possible while
// never cutting inside the required radius and never bulging more than tol outward.
func simplify(z, r []float64, tol float64) []Point {
	n := len(z)
	out := []Point{{z[0], r[0]}}
	if n < 2 {
		return out
	}
	if tol < 0 {
		tol = 0
	}
	k := 0
	for k < n-1 {
		best := k + 1
		for m := k + 2; m < n; m++ {
			ok := true
			for j := k + 1; j < m; j++ {
				t := (z[j] - z[k]) / (z[m] - z[k])
				lr := r[k] + t*(r[m]-r[k])
				if lr < r[j]-1e-9 || lr > r[j]+tol+1e-9 {
					ok = false
					break
				}
			}
			if !ok {
				break
			}
			best = m
		}
		out = append(out, Point{z[best], r[best]})
		k = best
	}
	return out
}
