package ui

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"buddy-tower/internal/version"
)

var (
	cAccent  = lipgloss.Color("#B48CFF")
	cAccent2 = lipgloss.Color("#EE6FF8")
	cDim     = lipgloss.Color("#6C6F85")
	cText    = lipgloss.Color("#E6E6F0")
	cGood    = lipgloss.Color("#5EE6B0")
	cBad     = lipgloss.Color("#FF6B81")
	cTower   = lipgloss.Color("#FFB86C")
	cPanel   = lipgloss.Color("#3B3D57")

	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1E1E2E")).
			Background(cAccent).Padding(0, 1)
	styleFile      = lipgloss.NewStyle().Foreground(cDim).PaddingLeft(1)
	stylePanel     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cPanel).Padding(0, 1)
	stylePanelHead = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	styleDim       = lipgloss.NewStyle().Foreground(cDim)
	styleText      = lipgloss.NewStyle().Foreground(cText)
	styleGood      = lipgloss.NewStyle().Foreground(cGood)
	styleBad       = lipgloss.NewStyle().Foreground(cBad)
	styleTowerC    = lipgloss.NewStyle().Foreground(cTower)
	styleInputText = lipgloss.NewStyle().Foreground(cText)

	styleBtn = lipgloss.NewStyle().Foreground(cText).Background(lipgloss.Color("#313244")).
			Padding(0, 2).MarginRight(1)
	styleBtnFocus = lipgloss.NewStyle().Foreground(lipgloss.Color("#1E1E2E")).Background(cAccent2).
			Bold(true).Padding(0, 2).MarginRight(1)
	styleBtnOff = lipgloss.NewStyle().Foreground(lipgloss.Color("#585B70")).Background(lipgloss.Color("#232634")).
			Padding(0, 2).MarginRight(1)
)

func (m Model) button(id focusID, label string, enabled bool) string {
	switch {
	case !enabled:
		return styleBtnOff.Render(label)
	case m.focus == id:
		return styleBtnFocus.Render("▸ " + label)
	default:
		return styleBtn.Render(label)
	}
}

// View renders the whole screen.
func (m Model) View() string {
	w := m.width
	if w < 60 {
		w = 60
	}
	var b strings.Builder
	head := styleTitle.Render("▲ BUDDY TOWER") + styleFile.Render(filepath.Base(m.opts.File))
	if m.phase != phaseSelect {
		head += styleDim.Render("  ›  ") + styleText.Render(m.entries[m.plate].Label())
	}
	b.WriteString(head + "\n")
	if !m.compact() {
		b.WriteString("\n")
	}

	switch m.phase {
	case phaseSelect:
		b.WriteString(m.viewSelect(w))
	case phaseIdle:
		b.WriteString(m.viewIdle(w))
	case phaseRunning:
		b.WriteString(m.viewRunning(w))
	case phaseDone:
		b.WriteString(m.viewDone(w))
	}

	st := styleGood
	if m.statusErr {
		st = styleBad
	}
	b.WriteString("\n" + lipgloss.NewStyle().MaxWidth(w).Render(st.Render(m.status)))
	b.WriteString("\n" + m.footer(w))
	return b.String()
}

func (m Model) footer(w int) string {
	help := "←→ move · enter press · q quit"
	switch m.phase {
	case phaseDone:
		help = "tab section · ↑↓ field · ←→ button/toggle · enter press · a analyze · w write · q quit"
	case phaseSelect:
		help = "↑↓ choose plate · enter select · tab to buttons · a analyze · q quit"
	case phaseRunning:
		help = "c / esc cancel · q quit"
	}
	ver := "v" + version.Version
	if lipgloss.Width(help)+lipgloss.Width(ver)+1 > w {
		help = lipgloss.NewStyle().MaxWidth(w-lipgloss.Width(ver)-2).Render(help) + "…"
	}
	gap := max(1, w-lipgloss.Width(help)-lipgloss.Width(ver))
	return styleDim.Render(help + strings.Repeat(" ", gap) + ver)
}

func (m Model) viewSelect(w int) string {
	var rows []string
	rows = append(rows, stylePanelHead.Render("This 3MF contains several gcode files — pick one"), "")
	for i, e := range m.entries {
		mark := "  ○ "
		st := styleText
		if i == m.cursor {
			mark = "  ● "
			if m.focus == fPlates {
				st = lipgloss.NewStyle().Foreground(cAccent2).Bold(true)
			}
		}
		rows = append(rows, st.Render(fmt.Sprintf("%s%-14s %10s   %s", mark, e.Label(), humanBytes(e.Size), styleDim.Render(e.Name))))
	}
	rows = append(rows, "", m.button(fAnalyze, "Analyze", true)+m.button(fQuit, "Quit", true))
	return stylePanel.Width(min(w-2, 90)).Render(strings.Join(rows, "\n"))
}

func (m Model) viewIdle(w int) string {
	e := m.entries[m.plate]
	rows := []string{
		stylePanelHead.Render("Ready to analyze"),
		"",
		styleText.Render(fmt.Sprintf("%s  ·  %s of gcode", e.Name, humanBytes(e.Size))),
		styleDim.Render("The file is re-read from disk every time you press Analyze."),
		"",
	}
	btns := m.button(fAnalyze, "Analyze", true) + m.button(fCancel, "Cancel", false)
	if len(m.entries) > 1 {
		btns += m.button(fChangePlate, "Change plate", true)
	}
	btns += m.button(fQuit, "Quit", true)
	rows = append(rows, btns)
	return stylePanel.Width(min(w-2, 90)).Render(strings.Join(rows, "\n"))
}

func (m Model) viewRunning(w int) string {
	pct := 0.0
	if m.total > 0 {
		pct = float64(m.done) / float64(m.total)
	}
	el := time.Since(m.startedAt).Seconds()
	rate := 0.0
	if el > 0 {
		rate = float64(m.done) / el
	}
	eta := "—"
	if rate > 0 && m.done > 0 {
		eta = fmt.Sprintf("%.1fs left", float64(m.total-m.done)/rate)
	}
	title := "Analyzing layer times…"
	if m.cancelling {
		title = "Cancelling…"
	}
	rows := []string{
		stylePanelHead.Render(title),
		"",
		m.bar.ViewAs(pct),
		"",
		styleDim.Render(fmt.Sprintf("%s / %s   ·   %s/s   ·   %s", humanBytes(m.done), humanBytes(m.total), humanBytes(int64(rate)), eta)),
		"",
		m.button(fAnalyze, "Analyze", false) + m.button(fCancel, "Cancel", !m.cancelling) + m.button(fQuit, "Quit", true),
	}
	return stylePanel.Width(min(w-2, 90)).Render(strings.Join(rows, "\n"))
}

const settingsW = 38 // settings panel width including border

// compact is true when the terminal is too short for the roomy layout.
func (m Model) compact() bool { return m.height < 34 }

func (m Model) viewDone(w int) string {
	btns := m.button(fAnalyze, "Re-analyze", true) + m.button(fWrite, "Write STEP", len(m.design.Profile) >= 2)
	if len(m.entries) > 1 {
		btns += m.button(fChangePlate, "Change plate", true)
	}
	btns += m.button(fQuit, "Quit", true)

	// lines used outside the panels: title, buttons, status, footer (+ spacers when roomy)
	overhead := 4
	sep := "\n"
	if !m.compact() {
		overhead += 2
		sep = "\n\n"
	}
	budget := m.height - overhead - 1
	left := m.viewSettings(budget)
	rightW := w - lipgloss.Width(left) - 1
	var body string
	if rightW >= 40 {
		right := m.viewAnalysis(rightW-4, budget-2)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", stylePanel.Width(rightW-2).Render(right))
	} else {
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", styleDim.Render("widen the\nterminal to\nsee charts"))
	}
	return btns + sep + body
}

func (m Model) viewSettings(budget int) string {
	inner := settingsW - 4
	base := 16 // title + 10 inputs + toggle + output (2) + border
	roomy := budget >= base+3
	showPM := budget >= base+3+4
	gap := func(rows []string) []string {
		if roomy {
			return append(rows, "")
		}
		return rows
	}
	accent := lipgloss.NewStyle().Foreground(cAccent2)
	marker := func(f bool) string {
		if f {
			return accent.Render("▸ ")
		}
		return "  "
	}
	var rows []string
	rows = append(rows, stylePanelHead.Render("Tower rules"))
	for i := 0; i < inOutput; i++ {
		focused := m.focus == fInput0+focusID(i)
		ls := styleDim
		if focused {
			ls = accent
		}
		val := m.inputs[i].View()
		if m.errs[i] {
			val = styleBad.Render(m.inputs[i].Value() + " ✗")
		}
		rows = append(rows, marker(focused)+ls.Render(fmt.Sprintf("%-21s", inputLabels[i]))+lipgloss.NewStyle().Width(inner-23).Render(val))
	}
	rows = gap(rows)
	focused := m.focus == fToggle
	ls := styleDim
	if focused {
		ls = accent
	}
	opt := func(on bool, s string) string {
		if on {
			return lipgloss.NewStyle().Foreground(cGood).Bold(true).Render("◉ " + s)
		}
		return styleDim.Render("○ " + s)
	}
	rows = append(rows, marker(focused)+ls.Render("Times ")+opt(m.params.UseNominal, "unslowed")+" "+opt(!m.params.UseNominal, "as sliced"))
	rows = gap(rows)
	rows = append(rows, stylePanelHead.Render("Output file"))
	rows = append(rows, marker(m.focus == fInput0+inOutput)+m.inputs[inOutput].View())
	if showPM {
		pm := m.pm
		rows = append(rows, "", stylePanelHead.Render("Tower prints with (from 3MF)"))
		rows = append(rows, styleDim.Render(fmt.Sprintf("%d walls · %.2f/%.2f mm lines", pm.Walls, pm.OuterW, pm.InnerW)))
		rows = append(rows, styleDim.Render(fmt.Sprintf("walls %.0f/%.0f mm/s · %.0f%% @ %.0f", pm.VOuter, pm.VInner, pm.Density*100, pm.VInfill)))
	}
	panel := stylePanel
	if m.focus >= fToggle {
		panel = panel.BorderForeground(cAccent)
	}
	return panel.Width(settingsW - 2).Render(fitLines(rows, inner))
}

// fitLines joins rows, truncating each so nothing wraps.
func fitLines(rows []string, w int) string {
	t := lipgloss.NewStyle().MaxWidth(w)
	for i, r := range rows {
		rows[i] = t.Render(r)
	}
	return strings.Join(rows, "\n")
}

func (m Model) viewAnalysis(w, h int) string {
	r := m.res
	d := m.design
	p := m.params
	narrow := w < 60
	var rows []string
	slowed := 0
	for _, l := range r.Layers {
		if l.Slowed() {
			slowed++
		}
	}
	line := fmt.Sprintf("%d layers · %.1f mm · est. %s", len(r.Layers), r.Layers[len(r.Layers)-1].Z, dur(r.TotalTime()))
	if r.SlicerModelTime > 0 {
		line += styleDim.Render(fmt.Sprintf(" (slicer %s)", dur(r.SlicerModelTime)))
	}
	under := fmt.Sprintf("%d layers under %.4gs", d.NeedLayers, p.TargetTime)
	if d.NeedLayers > 0 {
		under = styleBad.Render(under)
	} else {
		under = styleGood.Render(under)
	}
	if slowed > 0 {
		under += styleDim.Render(fmt.Sprintf(" · %d slowed by cooling", slowed))
	}
	hasTower := len(d.Profile) >= 2

	// fixed lines: head, 2 stats, axis, tower head, info (+warning); optional: legend, 2 spacers
	fixed := 6
	if d.ShortCount > 0 {
		fixed++
	}
	avail := h - fixed
	extras := avail >= 3+5+2
	if extras {
		avail -= 3
	}
	tc, pc := 3, 2
	if avail > 5 {
		tc = min(9, max(3, avail*3/5))
		pc = min(6, max(2, avail-tc))
	}
	if !hasTower {
		tc = min(9, max(3, avail-1))
	}
	chartW := max(20, w-7)

	rows = append(rows, stylePanelHead.Render("Layer times"), styleText.Render(line), under)
	if extras {
		rows = append(rows, "")
	}
	rows = append(rows, timeChart(m, chartW, tc, extras, narrow)...)
	if extras {
		rows = append(rows, "")
	}
	th := "Tower" + styleDim.Render("  (lying on its side · bed on the left)")
	if narrow {
		th = "Tower" + styleDim.Render(" (side view, bed left)")
	}
	rows = append(rows, stylePanelHead.Render(th))
	if !hasTower {
		rows = append(rows, styleGood.Render("No tower needed — every layer meets the minimum."))
		return fitLines(rows, w)
	}
	rows = append(rows, profileChart(m, chartW, pc)...)
	rows = append(rows, styleTowerC.Render(fmt.Sprintf("max Ø%.1f · %.1f mm tall · %d faces · %.1f cm³", d.MaxR*2, d.Height, len(d.Profile)+1, d.Volume/1000)))
	if d.ShortCount > 0 {
		rows = append(rows, styleBad.Render(fmt.Sprintf("⚠ %d layers short at Ø%.4g max — raise Max diameter", d.ShortCount, p.MaxDiameter)))
	}
	return fitLines(rows, w)
}

var eighths = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// timeChart draws min layer time per column, with the tower's added time stacked on top.
func timeChart(m Model, w, h int, legend, narrow bool) []string {
	r, d, p := m.res, m.design, m.params
	n := len(r.Layers)
	cols := min(w, n)
	part := make([]float64, cols)
	total := make([]float64, cols)
	for c := 0; c < cols; c++ {
		i0, i1 := c*n/cols, (c+1)*n/cols
		if i1 <= i0 {
			i1 = i0 + 1
		}
		pm, tm := math.Inf(1), math.Inf(1)
		for i := i0; i < i1; i++ {
			li := d.Layers[i]
			pm = math.Min(pm, li.PartTime)
			tm = math.Min(tm, li.PartTime+li.TowerTime+boolF(li.TowerTime > 0)*p.Overhead)
		}
		part[c], total[c] = pm, tm
	}
	top := math.Max(p.TargetTime*2.5, 1)
	targetRow := h - 1 - int(p.TargetTime/top*float64(h))
	partSt := lipgloss.NewStyle().Foreground(cAccent)
	lowSt := lipgloss.NewStyle().Foreground(cBad)
	towSt := lipgloss.NewStyle().Foreground(cTower)
	tgtSt := lipgloss.NewStyle().Foreground(cBad).Faint(true)
	var out []string
	for row := 0; row < h; row++ {
		hi := top * float64(h-row) / float64(h)
		lo := top * float64(h-row-1) / float64(h)
		var sb strings.Builder
		label := "      "
		switch row {
		case 0:
			label = fmt.Sprintf("%5.0fs", top)
		case targetRow:
			label = fmt.Sprintf("%5.4gs", p.TargetTime)
		case h - 1:
			label = "    0s"
		}
		sb.WriteString(styleDim.Render(label) + styleDim.Render("│"))
		for c := 0; c < cols; c++ {
			fp := (part[c] - lo) / (hi - lo)
			ft := (total[c] - lo) / (hi - lo)
			st := partSt
			if part[c] < p.TargetTime {
				st = lowSt
			}
			switch {
			case fp >= 1:
				sb.WriteString(st.Render("█"))
			case fp > 0:
				s := st
				if ft >= 1 {
					s = s.Background(cTower)
				}
				sb.WriteString(s.Render(string(eighths[int(fp*8)])))
			case ft >= 1:
				sb.WriteString(towSt.Render("█"))
			case ft > 0:
				sb.WriteString(towSt.Render(string(eighths[int(ft*8)])))
			case row == targetRow:
				sb.WriteString(tgtSt.Render("╌"))
			default:
				sb.WriteString(" ")
			}
		}
		out = append(out, sb.String())
	}
	axis := fmt.Sprintf("layer 1%s%d", strings.Repeat(" ", max(1, cols-7-len(fmt.Sprint(n)))), n)
	out = append(out, styleDim.Render("       "+axis))
	if legend {
		lg := partSt.Render("█ part") + "  " + lowSt.Render("█ part under limit") + "  " + towSt.Render("█ + tower") + "  " + tgtSt.Render("╌ limit")
		if narrow {
			lg = partSt.Render("█ part") + " " + lowSt.Render("█ under") + " " + towSt.Render("█ tower") + " " + tgtSt.Render("╌ limit")
		}
		out = append(out, "       "+lg)
	}
	return out
}

// profileChart draws the tower silhouette, columns aligned with the time chart.
func profileChart(m Model, w, h int) []string {
	r, d := m.res, m.design
	n := len(r.Layers)
	cols := min(w, n)
	rad := make([]float64, cols)
	for c := 0; c < cols; c++ {
		i0, i1 := c*n/cols, (c+1)*n/cols
		if i1 <= i0 {
			i1 = i0 + 1
		}
		for i := i0; i < i1; i++ {
			rad[c] = math.Max(rad[c], d.Layers[i].R)
		}
	}
	maxR := d.MaxR
	if maxR <= 0 {
		maxR = 1
	}
	sub := h * 2
	st := lipgloss.NewStyle().Foreground(cTower)
	var out []string
	for row := 0; row < h; row++ {
		var sb strings.Builder
		label := "      "
		if row == 0 {
			label = fmt.Sprintf("Ø%4.0f ", maxR*2)
		}
		sb.WriteString(styleDim.Render(label) + styleDim.Render("│"))
		for c := 0; c < cols; c++ {
			filled := func(k int) bool {
				y := maxR * (1 - 2*(float64(k)+0.5)/float64(sub))
				return rad[c] > 0 && math.Abs(y) <= rad[c]+1e-9
			}
			a, b := filled(row*2), filled(row*2+1)
			switch {
			case a && b:
				sb.WriteString(st.Render("█"))
			case a:
				sb.WriteString(st.Render("▀"))
			case b:
				sb.WriteString(st.Render("▄"))
			default:
				sb.WriteString(" ")
			}
		}
		out = append(out, sb.String())
	}
	return out
}

func boolF(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func dur(s float64) string {
	t := int(math.Round(s))
	h, m, sec := t/3600, (t%3600)/60, t%60
	if h > 0 {
		return fmt.Sprintf("%dh %02dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %02ds", m, sec)
	}
	return fmt.Sprintf("%ds", sec)
}

func humanBytes(n int64) string {
	f := float64(n)
	switch {
	case f >= 1<<30:
		return fmt.Sprintf("%.2f GB", f/(1<<30))
	case f >= 1<<20:
		return fmt.Sprintf("%.1f MB", f/(1<<20))
	case f >= 1<<10:
		return fmt.Sprintf("%.0f KB", f/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
