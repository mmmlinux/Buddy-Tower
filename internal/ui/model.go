// Package ui is the Bubble Tea front end for Buddy Tower.
package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"buddy-tower/internal/gcode"
	"buddy-tower/internal/step"
	"buddy-tower/internal/threemf"
	"buddy-tower/internal/tower"
)

type phase int

const (
	phaseSelect  phase = iota // choosing a plate
	phaseIdle                 // plate chosen, waiting for Analyze
	phaseRunning              // analysis in progress
	phaseDone                 // results shown
)

// focus targets
type focusID int

const (
	fPlates focusID = iota
	fAnalyze
	fCancel
	fWrite
	fChangePlate
	fQuit
	fToggle
	fInput0 // inputs follow
)

// setting inputs, in display order
const (
	inTarget = iota
	inMargin
	inMinD
	inMaxD
	inOverhang
	inShrink
	inFootD
	inFootH
	inOverhead
	inTol
	inOutput
	numInputs
)

var inputLabels = [numInputs]string{
	"Min layer time (s)",
	"Safety margin (%)",
	"Min diameter (mm)",
	"Max diameter (mm)",
	"Overhang ° from vert",
	"Narrow ° from vert",
	"Foot Ø (mm, 0=off)",
	"Foot height (mm)",
	"Travel overhead (s)",
	"Fit tolerance (mm)",
	"Output file",
}

// Sender delivers messages from background goroutines (tea.Program.Send).
type Sender struct{ Send func(tea.Msg) }

type progressMsg struct {
	run         int
	done, total int64
}

type doneMsg struct {
	run int
	res *gcode.Result
	err error
}

// Options configure the UI at launch.
type Options struct {
	File       string
	TargetTime float64 // 0 = use slicer's slow_down_layer_time
	Output     string
}

// Model is the Bubble Tea model.
type Model struct {
	opts    Options
	sender  *Sender
	entries []threemf.Entry
	plate   int // index into entries
	cursor  int // plate list cursor
	phase   phase
	focus   focusID

	run        int
	cancel     context.CancelFunc
	done       int64
	total      int64
	startedAt  time.Time
	bar        progress.Model
	cancelling bool

	res     *gcode.Result
	resName string // gcode entry the results came from
	pm      tower.PrintModel
	params  tower.Params
	design  tower.Design
	inputs  []textinput.Model
	errs    [numInputs]bool
	outSet  bool // user edited the output path

	lastFocus map[focusID]focusID // last item focused in each section, keyed by its first item

	status    string
	statusErr bool
	width     int
	height    int
}

// New builds the model. entries must contain at least one gcode.
func New(opts Options, entries []threemf.Entry, sender *Sender) Model {
	m := Model{
		opts:    opts,
		sender:  sender,
		entries: entries,
		bar:     progress.New(progress.WithGradient("#5A56E0", "#EE6FF8"), progress.WithWidth(50)),
		params:  tower.DefaultParams(opts.TargetTime),
		width:   100,
		height:  40,

		lastFocus: map[focusID]focusID{},
	}
	m.inputs = make([]textinput.Model, numInputs)
	for i := range m.inputs {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 256
		ti.Width = 10
		ti.PromptStyle = styleInputText
		ti.TextStyle = styleInputText
		m.inputs[i] = ti
	}
	m.inputs[inOutput].Width = settingsW - 8
	m.paramsToInputs()
	if opts.Output != "" {
		m.inputs[inOutput].SetValue(opts.Output)
		m.outSet = true
	}
	if len(entries) > 1 {
		m.phase = phaseSelect
		m.focus = fPlates
	} else {
		m.phase = phaseIdle
		m.focus = fAnalyze
	}
	m.setDefaultOutput()
	return m
}

// Init starts analysis right away when there is only one gcode.
func (m Model) Init() tea.Cmd {
	if len(m.entries) == 1 {
		return func() tea.Msg { return startMsg{} }
	}
	return nil
}

type startMsg struct{}

func fmtNum(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func (m *Model) paramsToInputs() {
	p := m.params
	vals := [numInputs - 1]float64{p.TargetTime, p.MarginPct, p.MinDiameter, p.MaxDiameter, p.OverhangDeg, p.ShrinkDeg, p.FootDiameter, p.FootHeight, p.Overhead, p.Tolerance}
	for i, v := range vals {
		m.inputs[i].SetValue(fmtNum(v))
	}
}

// inputsToParams parses all numeric inputs, marking bad ones.
func (m *Model) inputsToParams() {
	ptrs := [numInputs - 1]*float64{&m.params.TargetTime, &m.params.MarginPct, &m.params.MinDiameter, &m.params.MaxDiameter, &m.params.OverhangDeg, &m.params.ShrinkDeg, &m.params.FootDiameter, &m.params.FootHeight, &m.params.Overhead, &m.params.Tolerance}
	for i, p := range ptrs {
		v, err := strconv.ParseFloat(strings.TrimSpace(m.inputs[i].Value()), 64)
		bad := err != nil || v < 0
		switch i {
		case inTarget, inMaxD:
			bad = bad || v <= 0
		case inOverhang, inShrink:
			bad = bad || v < 1 || v > 85
		}
		m.errs[i] = bad
		if !bad {
			*p = v
		}
	}
}

func (m *Model) setDefaultOutput() {
	if m.outSet {
		return
	}
	m.inputs[inOutput].SetValue(threemf.TowerPath(m.opts.File, m.entries[m.plate], len(m.entries) > 1))
}

func (m *Model) rebuild() {
	if m.res == nil {
		return
	}
	m.inputsToParams()
	m.design = tower.Build(m.res.Layers, m.pm, m.params)
}

// sections groups the focusable items of the current phase. Tab jumps between
// sections; the arrow keys move within one.
func (m Model) sections() [][]focusID {
	multi := len(m.entries) > 1
	switch m.phase {
	case phaseSelect:
		return [][]focusID{{fPlates}, {fAnalyze, fQuit}}
	case phaseIdle:
		f := []focusID{fAnalyze}
		if multi {
			f = append(f, fChangePlate)
		}
		return [][]focusID{append(f, fQuit)}
	case phaseRunning:
		return [][]focusID{{fCancel, fQuit}}
	default:
		btns := []focusID{fAnalyze, fWrite}
		if multi {
			btns = append(btns, fChangePlate)
		}
		btns = append(btns, fQuit)
		var rules []focusID
		for i := 0; i < inOutput; i++ {
			rules = append(rules, fInput0+focusID(i))
		}
		rules = append(rules, fToggle)
		return [][]focusID{btns, rules, {fInput0 + inOutput}}
	}
}

// focusables lists every focusable item in the current phase.
func (m Model) focusables() []focusID {
	var out []focusID
	for _, sec := range m.sections() {
		out = append(out, sec...)
	}
	return out
}

// where returns the section index and position of the focused item.
func (m Model) where() (int, int) {
	for si, sec := range m.sections() {
		for i, f := range sec {
			if f == m.focus {
				return si, i
			}
		}
	}
	return 0, 0
}

// moveFocus moves within the current section, wrapping around.
func (m *Model) moveFocus(delta int) {
	secs := m.sections()
	si, i := m.where()
	sec := secs[si]
	m.setFocus(sec[(i+delta+len(sec))%len(sec)])
}

// nextSection jumps to the next (or previous) section, returning to the item
// that was last focused there.
func (m *Model) nextSection(delta int) {
	secs := m.sections()
	si, _ := m.where()
	m.lastFocus[secs[si][0]] = m.focus
	ni := (si + delta + len(secs)) % len(secs)
	target := secs[ni][0]
	if last, ok := m.lastFocus[target]; ok {
		for _, f := range secs[ni] {
			if f == last {
				target = last
			}
		}
	}
	m.setFocus(target)
}

func (m *Model) setFocus(f focusID) {
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
	m.focus = f
	if f >= fInput0 {
		m.inputs[f-fInput0].Focus()
		m.inputs[f-fInput0].CursorEnd()
	}
}

func (m *Model) ensureFocusValid() {
	for _, f := range m.focusables() {
		if f == m.focus {
			return
		}
	}
	m.setFocus(m.focusables()[0])
}

func (m *Model) setStatus(s string, isErr bool) {
	m.status, m.statusErr = s, isErr
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.bar.Width = clamp(m.width-24, 20, 80)
		return m, nil

	case startMsg:
		return m.startAnalysis()

	case progressMsg:
		if msg.run == m.run && m.phase == phaseRunning {
			m.done, m.total = msg.done, msg.total
		}
		return m, nil

	case doneMsg:
		if msg.run != m.run {
			return m, nil
		}
		m.cancel = nil
		m.cancelling = false
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				m.setStatus("Analysis cancelled.", false)
			} else {
				m.setStatus("Analysis failed: "+msg.err.Error(), true)
			}
			if m.res != nil && m.resName == m.entries[m.plate].Name {
				m.phase = phaseDone
			} else {
				m.phase = phaseIdle
			}
			m.setFocus(fAnalyze)
			return m, nil
		}
		first := m.res == nil
		m.res = msg.res
		m.resName = m.entries[m.plate].Name
		m.pm = tower.ModelFromSettings(msg.res.Settings)
		if first && m.opts.TargetTime <= 0 {
			t := m.pm.SlowDownLayerTime
			if t <= 0 {
				t = 8
			}
			m.inputs[inTarget].SetValue(fmtNum(t))
		}
		m.phase = phaseDone
		m.rebuild()
		m.setStatus(fmt.Sprintf("Analyzed %s in %.1fs (%s lines).", m.entries[m.plate].Label(), msg.res.Elapsed.Seconds(), groupInt(msg.res.Lines)), false)
		m.setFocus(fWrite)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	switch key {
	case "ctrl+c":
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	case "tab":
		m.nextSection(1)
		return m, nil
	case "shift+tab":
		m.nextSection(-1)
		return m, nil
	case "down", "up":
		delta := 1
		if key == "up" {
			delta = -1
		}
		if m.focus == fPlates {
			m.cursor = (m.cursor + delta + len(m.entries)) % len(m.entries)
			return m, nil
		}
		m.moveFocus(delta)
		return m, nil
	case "esc":
		if m.phase == phaseRunning {
			return m.doCancel()
		}
		if m.focus >= fInput0 {
			m.setFocus(fWrite)
		}
		return m, nil
	}

	if m.focus >= fInput0 {
		if key == "enter" {
			m.moveFocus(1)
			m.rebuild()
			return m, nil
		}
		i := int(m.focus - fInput0)
		var cmd tea.Cmd
		before := m.inputs[i].Value()
		m.inputs[i], cmd = m.inputs[i].Update(k)
		if m.inputs[i].Value() != before {
			if i == inOutput {
				m.outSet = true
			}
			m.rebuild()
		}
		return m, cmd
	}

	// shortcuts when not typing in a field
	switch key {
	case "q":
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	case "a":
		if m.phase != phaseRunning {
			return m.startAnalysis()
		}
	case "c":
		if m.phase == phaseRunning {
			return m.doCancel()
		}
	case "w":
		if m.phase == phaseDone {
			return m.writeStep()
		}
	case "left", "right":
		if m.focus == fToggle {
			m.params.UseNominal = !m.params.UseNominal
			m.rebuild()
			return m, nil
		}
		if m.focus < fToggle && m.focus != fPlates {
			if key == "left" {
				m.moveFocus(-1)
			} else {
				m.moveFocus(1)
			}
		}
		return m, nil
	case "enter", " ":
		return m.activate()
	}
	return m, nil
}

func (m Model) activate() (tea.Model, tea.Cmd) {
	switch m.focus {
	case fPlates:
		m.plate = m.cursor
		m.setDefaultOutput()
		m.setFocus(fAnalyze)
	case fAnalyze:
		return m.startAnalysis()
	case fCancel:
		return m.doCancel()
	case fWrite:
		return m.writeStep()
	case fChangePlate:
		m.phase = phaseSelect
		m.cursor = m.plate
		m.setFocus(fPlates)
	case fQuit:
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	case fToggle:
		m.params.UseNominal = !m.params.UseNominal
		m.rebuild()
	}
	return m, nil
}

func (m Model) doCancel() (tea.Model, tea.Cmd) {
	if m.cancel != nil && !m.cancelling {
		m.cancel()
		m.cancelling = true
		m.setStatus("Cancelling…", false)
	}
	return m, nil
}

// startAnalysis re-opens the 3MF from disk and streams the chosen gcode.
func (m Model) startAnalysis() (tea.Model, tea.Cmd) {
	if m.phase == phaseRunning {
		return m, nil
	}
	if m.phase == phaseSelect {
		m.plate = m.cursor
	}
	// re-read the archive's table of contents so a re-exported file is picked up
	want := m.entries[m.plate].Name
	entries, err := threemf.ListGcodes(m.opts.File)
	if err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	if len(entries) == 0 {
		m.setStatus("The file no longer contains any gcode.", true)
		return m, nil
	}
	m.entries = entries
	found := false
	for i, e := range entries {
		if e.Name == want {
			m.plate, found = i, true
		}
	}
	if !found {
		m.plate, m.cursor = 0, 0
		m.phase = phaseSelect
		m.setFocus(fPlates)
		m.setStatus(want+" is no longer in the file — pick a plate.", true)
		return m, nil
	}
	if m.cursor >= len(entries) {
		m.cursor = m.plate
	}
	m.setDefaultOutput()
	m.run++
	run := m.run
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.cancelling = false
	m.phase = phaseRunning
	m.done, m.total = 0, m.entries[m.plate].Size
	m.startedAt = time.Now()
	m.setFocus(fCancel)
	m.setStatus("", false)
	file, name := m.opts.File, m.entries[m.plate].Name
	send := m.sender.Send
	go func() {
		rc, err := threemf.Open(file, name)
		if err != nil {
			send(doneMsg{run: run, err: err})
			return
		}
		total := rc.Size
		res, err := gcode.Analyze(ctx, rc, total, func(done int64) {
			if ctx.Err() == nil {
				send(progressMsg{run: run, done: done, total: total})
			}
		})
		rc.Close()
		send(doneMsg{run: run, res: res, err: err})
	}()
	return m, nil
}

func (m Model) writeStep() (tea.Model, tea.Cmd) {
	m.rebuild()
	if len(m.design.Profile) < 2 {
		m.setStatus("Nothing to write: every layer already meets the minimum time.", true)
		return m, nil
	}
	path := strings.TrimSpace(m.inputs[inOutput].Value())
	if path == "" {
		m.setStatus("Set an output file first.", true)
		return m, nil
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	f, err := os.Create(path)
	if err != nil {
		m.setStatus("Could not write: "+err.Error(), true)
		return m, nil
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	err = step.Write(f, name, m.design.Profile)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		m.setStatus("Could not write: "+err.Error(), true)
		return m, nil
	}
	m.setStatus(fmt.Sprintf("Saved %s  (%d faces, %.1f mm tall, max Ø%.1f mm)", path, len(m.design.Profile)+1, m.design.Height, m.design.MaxR*2), false)
	return m, nil
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func groupInt(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}
