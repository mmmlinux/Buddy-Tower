// Buddy Tower: reads a sliced .gcode.3mf, measures every layer's print time and
// designs a round companion tower that keeps every layer above a minimum time.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"buddy-tower/internal/gcode"
	"buddy-tower/internal/step"
	"buddy-tower/internal/threemf"
	"buddy-tower/internal/tower"
	"buddy-tower/internal/ui"
	"buddy-tower/internal/version"
)

func main() {
	minTime := flag.Float64("min-time", 0, "minimum layer time in seconds (default: the slicer's slow_down_layer_time)")
	out := flag.String("out", "", "output STEP path (default: next to the 3MF)")
	plate := flag.String("plate", "", "gcode to use when the 3MF has several (e.g. plate_2)")
	stepOnly := flag.Bool("step", false, "no TUI: analyze, write the tower STEP file and exit")
	dump := flag.Bool("dump", false, "no TUI: print per-layer times and the tower design, write STEP if -out is set")
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Buddy Tower %s\n\nUsage: buddy-tower [flags] <file.gcode.3mf>\n\n", version.Version)
		flag.PrintDefaults()
	}
	// allow flags before or after the file name
	var pos []string
	args := os.Args[1:]
	for {
		flag.CommandLine.Parse(args)
		if flag.NArg() == 0 {
			break
		}
		pos = append(pos, flag.Arg(0))
		args = flag.Args()[1:]
	}
	if *showVer {
		fmt.Println(version.Version)
		return
	}
	if len(pos) != 1 {
		flag.Usage()
		os.Exit(2)
	}
	file := pos[0]
	entries, err := threemf.ListGcodes(file)
	if err != nil {
		fail(err)
	}
	if len(entries) == 0 {
		fail(fmt.Errorf("%s has no gcode inside; slice it and export the plate as a .gcode.3mf", filepath.Base(file)))
	}
	multi := len(entries) > 1
	if *plate != "" {
		var keep []threemf.Entry
		for _, e := range entries {
			if e.Label() == *plate || e.Name == *plate {
				keep = append(keep, e)
			}
		}
		if len(keep) == 0 {
			fail(fmt.Errorf("no gcode named %q in %s", *plate, file))
		}
		entries = keep
	}

	if *stepOnly {
		if len(entries) > 1 {
			var names []string
			for _, e := range entries {
				names = append(names, e.Label())
			}
			fail(fmt.Errorf("%s has several plates; choose one with -plate (%s)", filepath.Base(file), strings.Join(names, ", ")))
		}
		path := *out
		if path == "" {
			path = threemf.TowerPath(file, entries[0], multi)
		}
		runStep(file, entries[0], *minTime, path)
		return
	}
	if *dump {
		runDump(file, entries[0], *minTime, *out)
		return
	}

	sender := &ui.Sender{}
	m := ui.New(ui.Options{File: file, TargetTime: *minTime, Output: *out}, entries, sender)
	p := tea.NewProgram(m, tea.WithAltScreen())
	sender.Send = p.Send
	if _, err := p.Run(); err != nil {
		fail(err)
	}
}

func runDump(file string, e threemf.Entry, minTime float64, out string) {
	rc, err := threemf.Open(file, e.Name)
	if err != nil {
		fail(err)
	}
	res, err := gcode.Analyze(context.Background(), rc, rc.Size, nil)
	rc.Close()
	if err != nil {
		fail(err)
	}
	pm := tower.ModelFromSettings(res.Settings)
	if minTime <= 0 {
		minTime = pm.SlowDownLayerTime
	}
	d := tower.Build(res.Layers, pm, tower.DefaultParams(minTime))
	fmt.Printf("# %s %s: %d layers, est %.0fs (slicer %.0fs), analyzed in %v\n", filepath.Base(file), e.Label(), len(res.Layers), res.TotalTime(), res.SlicerModelTime, res.Elapsed)
	fmt.Println("layer,z,height,time,nominal_time,slow_factor,need,tower_r,tower_time")
	for i, l := range res.Layers {
		li := d.Layers[i]
		fmt.Printf("%d,%.3f,%.3f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f\n", l.Num, l.Z, l.Height, l.Time, l.NominalTime, l.SlowFactor, li.Need, li.R, li.TowerTime)
	}
	fmt.Printf("# tower: %d profile points, height %.2f, max dia %.2f, short layers %d\n", len(d.Profile), d.Height, d.MaxR*2, d.ShortCount)
	for _, p := range d.Profile {
		fmt.Printf("#   z=%.3f r=%.3f\n", p.Z, p.R)
	}
	if out != "" && len(d.Profile) >= 2 {
		f, err := os.Create(out)
		if err != nil {
			fail(err)
		}
		if err := step.Write(f, strings.TrimSuffix(filepath.Base(out), filepath.Ext(out)), d.Profile); err != nil {
			fail(err)
		}
		f.Close()
		fmt.Printf("# wrote %s\n", out)
	}
}

// runStep analyzes the plate and writes the tower without the TUI.
func runStep(file string, e threemf.Entry, minTime float64, out string) {
	rc, err := threemf.Open(file, e.Name)
	if err != nil {
		fail(err)
	}
	var progress gcode.Progress
	if fi, err := os.Stderr.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		total := rc.Size
		progress = func(done int64) {
			fmt.Fprintf(os.Stderr, "\rAnalyzing %s… %3.0f%%", e.Label(), 100*float64(done)/float64(max(total, 1)))
		}
	}
	res, err := gcode.Analyze(context.Background(), rc, rc.Size, progress)
	rc.Close()
	if progress != nil {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
	if err != nil {
		fail(err)
	}
	pm := tower.ModelFromSettings(res.Settings)
	if minTime <= 0 {
		minTime = pm.SlowDownLayerTime
	}
	if minTime <= 0 {
		minTime = 8
	}
	d := tower.Build(res.Layers, pm, tower.DefaultParams(minTime))
	fmt.Printf("%s: %d layers, %d under %.4gs\n", e.Label(), len(res.Layers), d.NeedLayers, minTime)
	if len(d.Profile) < 2 {
		fmt.Println("No tower needed: every layer already meets the minimum time. Nothing written.")
		return
	}
	f, err := os.Create(out)
	if err != nil {
		fail(err)
	}
	err = step.Write(f, strings.TrimSuffix(filepath.Base(out), filepath.Ext(out)), d.Profile)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		fail(err)
	}
	fmt.Printf("Wrote %s (%.1f mm tall, max Ø%.1f mm, %d faces)\n", out, d.Height, d.MaxR*2, len(d.Profile)+1)
	if d.ShortCount > 0 {
		fmt.Fprintf(os.Stderr, "warning: %d layers are still under the limit at the %.4g mm max diameter\n", d.ShortCount, tower.DefaultParams(minTime).MaxDiameter)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "buddy-tower:", err)
	os.Exit(1)
}
