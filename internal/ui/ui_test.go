package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"buddy-tower/internal/threemf"
)

const sample = "../../examplegcode.gcode.3mf"

func drive(t *testing.T, m Model, ch chan tea.Msg, until func(Model) bool) Model {
	t.Helper()
	deadline := time.After(60 * time.Second)
	for !until(m) {
		select {
		case msg := <-ch:
			nm, _ := m.Update(msg)
			m = nm.(Model)
		case <-deadline:
			t.Fatal("timeout")
		}
	}
	return m
}

func setup(t *testing.T) (Model, chan tea.Msg) {
	if _, err := os.Stat(sample); err != nil {
		t.Skip("sample file not present")
	}
	entries, err := threemf.ListGcodes(sample)
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan tea.Msg, 1000)
	s := &Sender{Send: func(msg tea.Msg) { ch <- msg }}
	m := New(Options{File: sample}, entries, s)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 50})
	return nm.(Model), ch
}

func TestAnalyzeAndRender(t *testing.T) {
	m, ch := setup(t)
	if m.Init() == nil {
		t.Fatal("single gcode should auto-start")
	}
	nm, _ := m.Update(startMsg{})
	m = nm.(Model)
	m = drive(t, m, ch, func(m Model) bool { return m.phase == phaseDone || (m.phase != phaseRunning) })
	if m.phase != phaseDone {
		t.Fatalf("phase %v status %q", m.phase, m.status)
	}
	if len(m.design.Profile) < 2 {
		t.Fatal("expected a tower")
	}
	if os.Getenv("SHOW") != "" {
		os.WriteFile(os.Getenv("SHOW"), []byte(m.View()), 0o644)
	}
	// edit a setting and toggle
	m.setFocus(fInput0 + inMaxD)
	before := m.design.MaxR
	for _, r := range []tea.KeyMsg{{Type: tea.KeyBackspace}, {Type: tea.KeyBackspace}, {Type: tea.KeyRunes, Runes: []rune("30")}} {
		nm, _ = m.Update(r)
		m = nm.(Model)
	}
	if m.design.MaxR > 15+1e-9 || m.design.MaxR >= before {
		t.Fatalf("max diameter edit not applied: %v -> %v", before, m.design.MaxR)
	}
	if m.design.ShortCount == 0 {
		t.Fatal("expected short layers at Ø30")
	}
	_ = m.View()
}

func TestCancel(t *testing.T) {
	m, ch := setup(t)
	nm, _ := m.Update(startMsg{})
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	if !m.cancelling {
		t.Fatal("esc should cancel")
	}
	m = drive(t, m, ch, func(m Model) bool { return m.phase != phaseRunning })
	if m.phase != phaseIdle || m.status != "Analysis cancelled." {
		t.Fatalf("phase %v status %q", m.phase, m.status)
	}
	_ = m.View()
}

func TestFitsTerminal(t *testing.T) {
	m, ch := setup(t)
	nm, _ := m.Update(startMsg{})
	m = nm.(Model)
	m = drive(t, m, ch, func(m Model) bool { return m.phase != phaseRunning })
	for _, sz := range [][2]int{{80, 24}, {100, 30}, {130, 50}, {200, 60}, {60, 20}} {
		nm, _ := m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		mm := nm.(Model)
		v := mm.View()
		lines := strings.Split(v, "\n")
		if len(lines) > sz[1] {
			t.Errorf("%dx%d: %d lines", sz[0], sz[1], len(lines))
		}
		for i, l := range lines {
			if lipgloss.Width(l) > max(sz[0], 60) {
				t.Errorf("%dx%d: line %d is %d wide", sz[0], sz[1], i, lipgloss.Width(l))
			}
		}
		if !strings.Contains(v, "Write STEP") || !strings.Contains(v, "BUDDY TOWER") {
			t.Errorf("%dx%d: buttons or title missing", sz[0], sz[1])
		}
		if dir := os.Getenv("SHOWDIR"); dir != "" {
			os.WriteFile(fmt.Sprintf("%s/view_%dx%d.txt", dir, sz[0], sz[1]), []byte(v), 0o644)
		}
	}
}

func TestTabMovesBetweenSections(t *testing.T) {
	m, ch := setup(t)
	nm, _ := m.Update(startMsg{})
	m = nm.(Model)
	m = drive(t, m, ch, func(m Model) bool { return m.phase != phaseRunning })
	press := func(k tea.KeyMsg) {
		nm, _ := m.Update(k)
		m = nm.(Model)
	}
	tab := tea.KeyMsg{Type: tea.KeyTab}
	if m.focus != fWrite {
		t.Fatalf("start focus %v", m.focus)
	}
	press(tab)
	if m.focus != fInput0+inTarget {
		t.Fatalf("tab from buttons should reach first rule, got %v", m.focus)
	}
	press(tea.KeyMsg{Type: tea.KeyDown})
	press(tea.KeyMsg{Type: tea.KeyDown})
	if m.focus != fInput0+inMinD {
		t.Fatalf("down should move within rules, got %v", m.focus)
	}
	press(tab)
	if m.focus != fInput0+inOutput {
		t.Fatalf("tab should reach output, got %v", m.focus)
	}
	press(tab)
	if m.focus != fWrite {
		t.Fatalf("tab should wrap to buttons and remember Write, got %v", m.focus)
	}
	press(tea.KeyMsg{Type: tea.KeyRight})
	if m.focus != fQuit {
		t.Fatalf("right should move along buttons, got %v", m.focus)
	}
	press(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.focus != fInput0+inOutput {
		t.Fatalf("shift+tab should go back to output, got %v", m.focus)
	}
	press(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.focus != fInput0+inMinD {
		t.Fatalf("returning to rules should restore last field, got %v", m.focus)
	}
}
