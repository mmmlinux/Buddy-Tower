// Package gcode streams a gcode file and estimates how long each layer takes to print.
package gcode

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Layer is the timing result for one printed layer.
type Layer struct {
	Num    int     // 1-based layer number
	Z      float64 // top of the layer (mm)
	Height float64 // layer thickness (mm)
	// Time is the estimated time of the layer as sliced (seconds).
	Time float64
	// NominalTime is the estimated time with the slicer's cooling slowdown removed.
	// This is how fast the part's layer will print once a tower adds enough time.
	NominalTime float64
	// SlowFactor is the detected cooling slowdown (1 = full speed, 0.5 = half speed).
	SlowFactor float64
}

// Slowed reports whether the slicer stretched this layer for cooling.
func (l Layer) Slowed() bool { return l.SlowFactor < 0.97 }

// Result is everything learned from one pass over the file.
type Result struct {
	Layers   []Layer
	Settings Settings
	// StartTime and EndTime are the start/end gcode outside any layer.
	StartTime, EndTime float64
	// Slicer's own estimates from the header (0 when absent).
	SlicerModelTime, SlicerTotalTime float64
	Bytes                            int64
	Lines                            int
	Elapsed                          time.Duration
}

// TotalTime is the sum of all estimated layer times.
func (r *Result) TotalTime() float64 {
	t := 0.0
	for _, l := range r.Layers {
		t += l.Time
	}
	return t
}

// Progress is called periodically with the number of bytes processed.
type Progress func(done int64)

// ErrNoLayers is returned when no layer markers were found.
var ErrNoLayers = errors.New("no layer change markers found in gcode")

type move struct {
	dist   float64
	vReq   float64 // requested feedrate (mm/s) after machine caps
	accel  float64
	safe   float64    // max speed when starting/stopping from rest (jerk)
	u0, u1 [3]float64 // direction at start and end of move (unit)
	feat   int16
	extr   bool
	jerk   [3]float64
	capV   float64 // machine speed cap along this direction
}

type analyzer struct {
	settings Settings

	// machine limits
	maxV         [4]float64 // x y z e
	maxA         [4]float64
	jerk         [4]float64
	maxAccExtr   float64
	maxAccTravel float64
	maxAccRetr   float64

	// modal state
	pos      [4]float64
	relXYZ   bool
	relE     bool
	feed     float64 // mm/min
	accel    float64
	feature  int16
	features []string
	featIdx  map[string]int16
	featV    []float64 // configured speed (mm/s) per feature, 0 = not adjustable

	// current layer
	inLayer  bool
	inConfig bool
	ended    bool
	moves    []move
	layer    Layer
	dwell    float64 // dwell time accumulated in current layer
	planned  float64 // time already planned (flushed) in the current segment
	nominal  float64
	slowNum  float64 // sum(len * ratio) for reference features
	slowDen  float64
	layerCap float64  // fastest adjustable extrusion speed seen in this layer
	uniform  bool     // slicer uses Bambu's uniform_cooling slowdown logic
	segments [][]move // flushed segments of this layer (split by dwells)

	res Result
}

var reDur = regexp.MustCompile(`(\d+)\s*([dhms])`)

func parseDuration(s string) float64 {
	t := 0.0
	for _, m := range reDur.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.ParseFloat(m[1], 64)
		switch m[2] {
		case "d":
			t += n * 86400
		case "h":
			t += n * 3600
		case "m":
			t += n * 60
		case "s":
			t += n
		}
	}
	return t
}

// Analyze reads gcode from r (total bytes, for progress) and returns per-layer times.
func Analyze(ctx context.Context, r io.Reader, total int64, progress Progress) (*Result, error) {
	start := time.Now()
	a := &analyzer{
		settings: Settings{},
		feed:     3000,
		accel:    1000,
		featIdx:  map[string]int16{},
		feature:  -1,
	}
	a.res.Settings = a.settings
	a.setDefaults()

	br := bufio.NewReaderSize(r, 1<<20)
	var long []byte
	var done int64
	lastReport := time.Now()
	lines := 0
	for {
		line, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			long = append(long[:0], line...)
			for err == bufio.ErrBufferFull {
				line, err = br.ReadSlice('\n')
				long = append(long, line...)
			}
			line = long
		}
		done += int64(len(line))
		if len(line) > 0 {
			lines++
			a.line(line)
		}
		if lines&4095 == 0 {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if progress != nil && time.Since(lastReport) > 50*time.Millisecond {
				progress(done)
				lastReport = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	a.finishLayer()
	if progress != nil {
		progress(done)
	}
	if len(a.res.Layers) == 0 {
		return nil, ErrNoLayers
	}
	a.res.Bytes = done
	a.res.Lines = lines
	a.res.Elapsed = time.Since(start)
	_ = total
	return &a.res, nil
}

func (a *analyzer) setDefaults() {
	a.maxV = [4]float64{500, 500, 20, 30}
	a.maxA = [4]float64{20000, 20000, 500, 5000}
	a.jerk = [4]float64{9, 9, 3, 2.5}
	a.maxAccExtr, a.maxAccTravel, a.maxAccRetr = 20000, 9000, 5000
}

// applyMachineLimits reads machine limits once the config block is complete.
func (a *analyzer) applyMachineLimits() {
	s := a.settings
	axes := []string{"x", "y", "z", "e"}
	for i, ax := range axes {
		a.maxV[i] = s.FloatOr("machine_max_speed_"+ax, 0, a.maxV[i])
		a.maxA[i] = s.FloatOr("machine_max_acceleration_"+ax, 0, a.maxA[i])
		a.jerk[i] = s.FloatOr("machine_max_jerk_"+ax, 0, a.jerk[i])
	}
	a.maxAccExtr = s.FloatOr("machine_max_acceleration_extruding", 0, a.maxAccExtr)
	a.maxAccTravel = s.FloatOr("machine_max_acceleration_travel", 0, a.maxAccTravel)
	a.maxAccRetr = s.FloatOr("machine_max_acceleration_retracting", 0, a.maxAccRetr)
	a.uniform = strings.Contains(s["cooling_slowdown_logic"], "uniform")
	if acc, ok := s.Float("default_acceleration", 0); ok && acc > 0 {
		a.accel = acc
	}
	// re-resolve feature speeds now that settings are known
	for i, name := range a.features {
		a.featV[i] = a.featureSpeed(name)
	}
}

func (a *analyzer) featureSpeed(name string) float64 {
	if key, ok := featureSpeedKey[name]; ok {
		if v, ok := a.settings.Float(key, 0); ok {
			return v
		}
	}
	return 0
}

func (a *analyzer) featureID(name string) int16 {
	if id, ok := a.featIdx[name]; ok {
		return id
	}
	id := int16(len(a.features))
	a.features = append(a.features, name)
	a.featV = append(a.featV, a.featureSpeed(name))
	a.featIdx[name] = id
	return id
}

func trimLine(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	return b
}

func (a *analyzer) line(raw []byte) {
	b := trimLine(raw)
	if len(b) == 0 {
		return
	}
	if b[0] == ';' {
		a.comment(b)
		return
	}
	if i := bytes.IndexByte(b, ';'); i >= 0 {
		b = trimLine(b[:i])
	}
	if len(b) < 2 {
		return
	}
	switch b[0] {
	case 'G':
		code, rest := cmdNum(b[1:])
		switch code {
		case 0, 1:
			a.linear(rest)
		case 2, 3:
			a.arc(rest, code == 2)
		case 4:
			a.g4(rest)
		case 90:
			a.relXYZ, a.relE = false, false
		case 91:
			a.relXYZ, a.relE = true, true
		case 92:
			a.g92(rest)
		case 28:
			a.flushSegment()
		}
	case 'M':
		code, rest := cmdNum(b[1:])
		switch code {
		case 82:
			a.relE = false
		case 83:
			a.relE = true
		case 204:
			a.m204(rest)
		case 400:
			a.m400(rest)
		}
	}
}

// cmdNum parses the integer command number and returns the remaining bytes.
// It returns -1 for sub-codes like M620.1 so they are ignored.
func cmdNum(b []byte) (int, []byte) {
	n, i := 0, 0
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		n = n*10 + int(b[i]-'0')
		i++
	}
	if i == 0 {
		return -1, nil
	}
	if i < len(b) && b[i] == '.' {
		return -1, nil
	}
	return n, b[i:]
}

// params iterates "X1.2 Y3 E-.5" style words.
func params(b []byte, fn func(letter byte, v float64)) {
	i := 0
	for i < len(b) {
		c := b[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		if c < 'A' || c > 'Z' {
			i++
			continue
		}
		i++
		j := i
		for j < len(b) && b[j] != ' ' && b[j] != '\t' {
			j++
		}
		if v, ok := fastFloat(b[i:j]); ok {
			fn(c, v)
		}
		i = j
	}
}

// fastFloat parses simple decimal numbers without allocating.
func fastFloat(b []byte) (float64, bool) {
	if len(b) == 0 {
		return 0, false
	}
	neg := false
	i := 0
	if b[0] == '-' || b[0] == '+' {
		neg = b[0] == '-'
		i++
	}
	var mant float64
	digits := 0
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		mant = mant*10 + float64(b[i]-'0')
		i++
		digits++
	}
	if i < len(b) && b[i] == '.' {
		i++
		scale := 0.1
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			mant += float64(b[i]-'0') * scale
			scale *= 0.1
			i++
			digits++
		}
	}
	if i < len(b) || digits == 0 {
		// fall back for exponents or odd input
		f, err := strconv.ParseFloat(string(b), 64)
		return f, err == nil
	}
	if neg {
		mant = -mant
	}
	return mant, true
}

func (a *analyzer) comment(b []byte) {
	s := bytes.TrimSpace(b[1:])
	switch {
	case bytes.HasPrefix(s, []byte("FEATURE:")):
		a.feature = a.featureID(string(bytes.TrimSpace(s[8:])))
	case bytes.HasPrefix(s, []byte("TYPE:")):
		a.feature = a.featureID(string(bytes.TrimSpace(s[5:])))
	case bytes.Equal(s, []byte("CHANGE_LAYER")) || bytes.Equal(s, []byte("LAYER_CHANGE")):
		a.finishLayer()
		a.inLayer = true
		a.layer = Layer{Num: len(a.res.Layers) + 1}
	case bytes.HasPrefix(s, []byte("Z_HEIGHT:")):
		a.layer.Z, _ = fastFloat(bytes.TrimSpace(s[9:]))
	case bytes.HasPrefix(s, []byte("Z:")) && a.inLayer && a.layer.Z == 0:
		a.layer.Z, _ = fastFloat(bytes.TrimSpace(s[2:]))
	case bytes.HasPrefix(s, []byte("LAYER_HEIGHT:")):
		a.layer.Height, _ = fastFloat(bytes.TrimSpace(s[13:]))
	case bytes.HasPrefix(s, []byte("HEIGHT:")) && a.inLayer && a.layer.Height == 0:
		a.layer.Height, _ = fastFloat(bytes.TrimSpace(s[7:]))
	case bytes.HasPrefix(s, []byte("MACHINE_END_GCODE_START")) || bytes.HasPrefix(s, []byte("EXECUTABLE_BLOCK_END")):
		if a.inLayer {
			a.finishLayer()
			a.inLayer = false
			a.ended = true
		}
	case bytes.Equal(s, []byte("CONFIG_BLOCK_START")):
		a.inConfig = true
	case bytes.Equal(s, []byte("CONFIG_BLOCK_END")):
		a.inConfig = false
		a.applyMachineLimits()
	case bytes.HasPrefix(s, []byte("model printing time:")):
		str := string(s)
		if i := strings.Index(str, ";"); i >= 0 {
			a.res.SlicerModelTime = parseDuration(str[:i])
			if j := strings.Index(str[i:], ":"); j >= 0 {
				a.res.SlicerTotalTime = parseDuration(str[i+j:])
			}
		} else {
			a.res.SlicerModelTime = parseDuration(str)
		}
	default:
		if a.inConfig {
			if eq := bytes.Index(s, []byte(" = ")); eq > 0 {
				a.settings[string(s[:eq])] = string(s[eq+3:])
			} else if bytes.HasSuffix(s, []byte(" =")) {
				a.settings[string(s[:len(s)-2])] = ""
			}
		}
	}
}

func (a *analyzer) g92(rest []byte) {
	params(rest, func(c byte, v float64) {
		switch c {
		case 'X':
			a.pos[0] = v
		case 'Y':
			a.pos[1] = v
		case 'Z':
			a.pos[2] = v
		case 'E':
			a.pos[3] = v
		}
	})
}

func (a *analyzer) m204(rest []byte) {
	params(rest, func(c byte, v float64) {
		if (c == 'S' || c == 'P') && v > 0 {
			a.accel = v
		}
	})
}

func (a *analyzer) g4(rest []byte) {
	t := 0.0
	params(rest, func(c byte, v float64) {
		switch c {
		case 'S':
			t += v
		case 'P':
			t += v / 1000
		}
	})
	a.flushSegment()
	a.addDwell(t)
}

func (a *analyzer) m400(rest []byte) {
	t := 0.0
	params(rest, func(c byte, v float64) {
		switch c {
		case 'S':
			t += v
		case 'P':
			t += v / 1000
		}
	})
	a.flushSegment()
	a.addDwell(t)
}

func (a *analyzer) addDwell(t float64) {
	if a.inLayer {
		a.dwell += t
	} else if a.ended {
		a.res.EndTime += t
	} else {
		a.res.StartTime += t
	}
}

func (a *analyzer) target(rest []byte) (t [4]float64, has [4]bool) {
	t = a.pos
	params(rest, func(c byte, v float64) {
		idx := -1
		switch c {
		case 'X':
			idx = 0
		case 'Y':
			idx = 1
		case 'Z':
			idx = 2
		case 'E':
			idx = 3
		case 'F':
			if v > 0 {
				a.feed = v
			}
		}
		if idx < 0 {
			return
		}
		has[idx] = true
		rel := a.relXYZ
		if idx == 3 {
			rel = a.relE
		}
		if rel {
			t[idx] = a.pos[idx] + v
		} else {
			t[idx] = v
		}
	})
	return
}

func (a *analyzer) linear(rest []byte) {
	t, _ := a.target(rest)
	d := [3]float64{t[0] - a.pos[0], t[1] - a.pos[1], t[2] - a.pos[2]}
	de := t[3] - a.pos[3]
	a.pos = t
	dist := math.Sqrt(d[0]*d[0] + d[1]*d[1] + d[2]*d[2])
	if dist < 1e-6 {
		if math.Abs(de) < 1e-7 {
			return
		}
		// extruder-only move (retract / unretract)
		v := math.Min(a.feed/60, a.maxV[3])
		acc := math.Min(a.accel, a.maxAccRetr)
		acc = math.Min(acc, a.maxA[3])
		a.push(move{dist: math.Abs(de), vReq: v, capV: v, accel: acc, safe: math.Min(v, a.jerk[3]), feat: a.feature, extr: false})
		return
	}
	u := [3]float64{d[0] / dist, d[1] / dist, d[2] / dist}
	a.pushXYZ(dist, u, u, de > 1e-7)
}

func (a *analyzer) arc(rest []byte, cw bool) {
	start := a.pos
	var ci, cj float64
	var rr float64
	hasR := false
	params(rest, func(c byte, v float64) {
		switch c {
		case 'I':
			ci = v
		case 'J':
			cj = v
		case 'R':
			rr, hasR = v, true
		}
	})
	t, has := a.target(rest)
	a.pos = t
	de := t[3] - start[3]
	sx, sy := start[0], start[1]
	ex, ey := t[0], t[1]
	var cx, cy, r float64
	if hasR {
		// center from radius form
		dx, dy := ex-sx, ey-sy
		q := math.Hypot(dx, dy)
		if q < 1e-9 || math.Abs(rr) < q/2 {
			a.pushStraight(start, t, de)
			return
		}
		h := math.Sqrt(rr*rr - q*q/4)
		mx, my := (sx+ex)/2, (sy+ey)/2
		sgn := 1.0
		if cw != (rr < 0) {
			sgn = -1
		}
		cx = mx - sgn*h*dy/q
		cy = my + sgn*h*dx/q
		r = math.Abs(rr)
	} else {
		cx, cy = sx+ci, sy+cj
		r = math.Hypot(ci, cj)
	}
	if r < 1e-6 {
		a.pushStraight(start, t, de)
		return
	}
	a0 := math.Atan2(sy-cy, sx-cx)
	a1 := math.Atan2(ey-cy, ex-cx)
	var sweep float64
	if cw {
		sweep = a0 - a1
	} else {
		sweep = a1 - a0
	}
	for sweep <= 1e-9 {
		sweep += 2 * math.Pi
	}
	full := (!has[0] && !has[1]) || math.Hypot(ex-sx, ey-sy) < 1e-6
	if full {
		sweep = 2 * math.Pi
	}
	dz := t[2] - start[2]
	arcLen := r * sweep
	dist := math.Hypot(arcLen, dz)
	if dist < 1e-6 {
		return
	}
	// tangents at the start and end of the arc
	tan := func(px, py float64) [3]float64 {
		rx, ry := (px-cx)/r, (py-cy)/r
		var tx, ty float64
		if cw {
			tx, ty = ry, -rx
		} else {
			tx, ty = -ry, rx
		}
		k := arcLen / dist
		return [3]float64{tx * k, ty * k, dz / dist}
	}
	a.pushXYZ(dist, tan(sx, sy), tan(ex, ey), de > 1e-7)
}

func (a *analyzer) pushStraight(s, t [4]float64, de float64) {
	d := [3]float64{t[0] - s[0], t[1] - s[1], t[2] - s[2]}
	dist := math.Sqrt(d[0]*d[0] + d[1]*d[1] + d[2]*d[2])
	if dist < 1e-6 {
		return
	}
	u := [3]float64{d[0] / dist, d[1] / dist, d[2] / dist}
	a.pushXYZ(dist, u, u, de > 1e-7)
}

func (a *analyzer) pushXYZ(dist float64, u0, u1 [3]float64, extruding bool) {
	v := a.feed / 60
	capV := math.Inf(1)
	acc := a.accel
	if extruding {
		acc = math.Min(acc, a.maxAccExtr)
	} else {
		acc = math.Min(acc, a.maxAccTravel)
	}
	safe := math.Inf(1)
	for i := 0; i < 3; i++ {
		c := math.Max(math.Abs(u0[i]), math.Abs(u1[i]))
		if c > 1e-9 {
			capV = math.Min(capV, a.maxV[i]/c)
			acc = math.Min(acc, a.maxA[i]/c)
			safe = math.Min(safe, a.jerk[i]/c)
		}
	}
	v = math.Min(v, capV)
	a.push(move{dist: dist, vReq: v, capV: capV, accel: acc, safe: math.Min(v, safe), u0: u0, u1: u1, feat: a.feature, extr: extruding, jerk: [3]float64{a.jerk[0], a.jerk[1], a.jerk[2]}})
}

func (a *analyzer) push(m move) {
	if m.extr && a.inLayer && m.feat >= 0 && a.featV[m.feat] > 0 && m.vReq > a.layerCap {
		a.layerCap = m.vReq
	}
	if m.extr && a.inLayer && m.feat >= 0 && referenceFeatures[a.features[m.feat]] {
		if cfg := a.featV[m.feat]; cfg > 0 {
			ratio := m.vReq / cfg
			if ratio <= 1.05 {
				a.slowNum += m.dist * ratio
				a.slowDen += m.dist
			}
		}
	}
	a.moves = append(a.moves, m)
}

// flushSegment plans the buffered moves (which end at rest) and books their time.
func (a *analyzer) flushSegment() {
	if len(a.moves) == 0 {
		return
	}
	if a.inLayer {
		seg := make([]move, len(a.moves))
		copy(seg, a.moves)
		a.segments = append(a.segments, seg)
	} else {
		t := plan(a.moves, nil)
		if a.ended {
			a.res.EndTime += t
		} else {
			a.res.StartTime += t
		}
	}
	a.moves = a.moves[:0]
}

func (a *analyzer) finishLayer() {
	a.flushSegment()
	if !a.inLayer {
		return
	}
	factor, restore := a.slowdown()
	actual, nominal := a.dwell, a.dwell
	var boost []float64
	for _, seg := range a.segments {
		actual += plan(seg, nil)
		if restore != nil {
			boost = boost[:0]
			for _, m := range seg {
				boost = append(boost, restore(m))
			}
			nominal += plan(seg, boost)
		}
	}
	if restore == nil {
		nominal = actual
	}
	a.layer.Time = actual
	a.layer.NominalTime = nominal
	a.layer.SlowFactor = factor
	if a.layer.Height == 0 {
		prevZ := 0.0
		if n := len(a.res.Layers); n > 0 {
			prevZ = a.res.Layers[n-1].Z
		}
		a.layer.Height = a.layer.Z - prevZ
	}
	a.res.Layers = append(a.res.Layers, a.layer)
	a.segments = a.segments[:0]
	a.dwell, a.slowNum, a.slowDen, a.layerCap = 0, 0, 0, 0
	a.inLayer = false
}

// slowdown works out whether the slicer stretched this layer for cooling and, if so,
// returns a function giving each move's speed with that slowdown undone.
//
// Bambu's "uniform_cooling" logic caps every adjustable line at one speed for the
// layer, so lines sitting at that cap (and below their configured speed) were slowed.
// The older proportional logic scales lines by a common factor instead.
func (a *analyzer) slowdown() (float64, func(move) float64) {
	if a.layer.Num <= 1 {
		return 1, nil
	}
	if a.uniform {
		limit := a.layerCap
		if limit <= 0 {
			return 1, nil
		}
		slowed := false
		maxCfg := 0.0
		for _, seg := range a.segments {
			for _, m := range seg {
				if !m.extr || m.feat < 0 || !referenceFeatures[a.features[m.feat]] {
					continue
				}
				cfg := a.featV[m.feat]
				if cfg <= 0 {
					continue
				}
				maxCfg = math.Max(maxCfg, cfg)
				if m.vReq <= 0.97*cfg && m.vReq >= 0.98*limit && m.vReq < 0.97*m.capV {
					slowed = true
				}
			}
		}
		if !slowed || maxCfg <= 0 {
			return 1, nil
		}
		return math.Min(1, limit/maxCfg), func(m move) float64 {
			if m.extr && m.feat >= 0 {
				if cfg := a.featV[m.feat]; cfg > 0 && m.vReq >= 0.98*limit && m.vReq < cfg {
					return math.Min(cfg, m.capV)
				}
			}
			return m.vReq
		}
	}
	if a.slowDen <= 0 {
		return 1, nil
	}
	factor := math.Min(1, a.slowNum/a.slowDen)
	if factor >= 0.97 {
		return factor, nil
	}
	return factor, func(m move) float64 {
		if m.extr && m.feat >= 0 {
			if cfg := a.featV[m.feat]; cfg > 0 {
				return math.Min(math.Min(m.vReq/factor, cfg), m.capV)
			}
		}
		return m.vReq
	}
}

// plan runs a look-ahead trapezoid planner over moves that start and end at rest.
// speeds, when non-nil, overrides each move's requested speed.
func plan(ms []move, speeds []float64) float64 {
	n := len(ms)
	if n == 0 {
		return 0
	}
	vel := func(i int) float64 {
		if speeds != nil {
			return speeds[i]
		}
		return ms[i].vReq
	}
	// junction limits: entry[i] is the max speed at the start of move i
	entry := make([]float64, n+1)
	entry[0] = math.Min(ms[0].safe, vel(0))
	for i := 1; i < n; i++ {
		p, c := ms[i-1], ms[i]
		vp, vc := vel(i-1), vel(i)
		lim := math.Min(vp, vc)
		if p.extr != c.extr && (p.dist == 0 || c.dist == 0) {
			lim = math.Min(p.safe, c.safe)
		}
		// classic jerk: per-axis velocity change across the junction must stay under jerk
		for ax := 0; ax < 3; ax++ {
			dv := math.Abs(lim*c.u0[ax] - lim*p.u1[ax])
			if dv > c.jerk[ax] && c.jerk[ax] > 0 {
				lim = math.Min(lim, c.jerk[ax]/math.Abs(c.u0[ax]-p.u1[ax]))
			}
		}
		// E-only moves have zero direction vectors; treat junction as a stop/start
		if p.u1 == [3]float64{} || c.u0 == [3]float64{} {
			lim = math.Min(math.Min(p.safe, c.safe), lim)
		}
		entry[i] = math.Max(lim, math.Min(p.safe, c.safe))
		entry[i] = math.Min(entry[i], math.Min(vp, vc))
	}
	entry[n] = math.Min(ms[n-1].safe, vel(n-1))
	// backward pass: must be able to decelerate into the next junction
	for i := n - 1; i >= 0; i-- {
		m := ms[i]
		maxEntry := math.Sqrt(entry[i+1]*entry[i+1] + 2*m.accel*m.dist)
		if entry[i] > maxEntry {
			entry[i] = maxEntry
		}
	}
	// forward pass: limited by acceleration from the previous junction
	for i := 0; i < n; i++ {
		m := ms[i]
		maxExit := math.Sqrt(entry[i]*entry[i] + 2*m.accel*m.dist)
		if entry[i+1] > maxExit {
			entry[i+1] = maxExit
		}
	}
	total := 0.0
	for i := 0; i < n; i++ {
		total += trapezoid(ms[i].dist, entry[i], entry[i+1], vel(i), ms[i].accel)
	}
	return total
}

// trapezoid returns the time to travel d with entry v0, exit v1, cruise vmax, accel a.
func trapezoid(d, v0, v1, vmax, a float64) float64 {
	if d <= 0 {
		return 0
	}
	if vmax <= 0 {
		return 0
	}
	if a <= 0 {
		return d / vmax
	}
	if v0 > vmax {
		v0 = vmax
	}
	if v1 > vmax {
		v1 = vmax
	}
	da := (vmax*vmax - v0*v0) / (2 * a)
	dd := (vmax*vmax - v1*v1) / (2 * a)
	if da+dd <= d {
		return (vmax-v0)/a + (vmax-v1)/a + (d-da-dd)/vmax
	}
	vp := math.Sqrt((2*a*d + v0*v0 + v1*v1) / 2)
	if vp < math.Max(v0, v1) {
		vp = math.Max(v0, v1)
	}
	return math.Max(0, (vp-v0)/a) + math.Max(0, (vp-v1)/a)
}
