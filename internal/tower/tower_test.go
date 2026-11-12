package tower

import (
	"math"
	"testing"

	"buddy-tower/internal/gcode"
)

func testModel() PrintModel {
	return PrintModel{Walls: 4, OuterW: 0.42, InnerW: 0.45, InfillW: 0.45, Density: 0.15,
		VOuter: 60, VInner: 150, VInfill: 200, AOuter: 2000, AInner: 4000, AInfill: 4000}
}

// A print whose last 100 layers are fast must get a tower that meets the target
// on every layer while never widening faster than the overhang angle.
func TestBuildRespectsRules(t *testing.T) {
	var layers []gcode.Layer
	z := 0.0
	for i := 0; i < 600; i++ {
		h := 0.2
		z += h
		tm := 30.0
		if i >= 500 {
			tm = 2
		}
		if i == 300 {
			tm = 3 // single fast layer mid-print
		}
		layers = append(layers, gcode.Layer{Num: i + 1, Z: z, Height: h, Time: tm, NominalTime: tm, SlowFactor: 1})
	}
	m := testModel()
	p := DefaultParams(10)
	d := Build(layers, m, p)
	if d.ShortCount != 0 {
		t.Fatalf("%d short layers", d.ShortCount)
	}
	target := p.TargetTime * (1 + p.MarginPct/100)
	for i, li := range d.Layers {
		if li.Need > 0 && li.PartTime+li.TowerTime < target-1e-6 {
			t.Errorf("layer %d: %.2f+%.2f < %.2f", i+1, li.PartTime, li.TowerTime, target)
		}
	}
	tanO := math.Tan(p.OverhangDeg * math.Pi / 180)
	tanS := math.Tan(p.ShrinkDeg * math.Pi / 180)
	for k := 1; k < len(d.Profile); k++ {
		a, b := d.Profile[k-1], d.Profile[k]
		slope := (b.R - a.R) / (b.Z - a.Z)
		if slope > tanO+1e-6 {
			t.Errorf("segment %d widens too steeply: %.3f", k, slope)
		}
		if -slope > tanS+1e-6 {
			t.Errorf("segment %d narrows too steeply: %.3f", k, slope)
		}
		if b.R < p.MinDiameter/2-1e-9 {
			t.Errorf("segment %d thinner than min", k)
		}
	}
	if d.Profile[0].R != p.FootDiameter/2 {
		t.Errorf("foot radius %.2f", d.Profile[0].R)
	}
	if math.Abs(d.Height-layers[len(layers)-1].Z) > 1e-9 {
		t.Errorf("height %.2f", d.Height)
	}
}

func TestNoTowerNeeded(t *testing.T) {
	layers := []gcode.Layer{{Num: 1, Z: 0.2, Height: 0.2, Time: 50, NominalTime: 50}}
	if d := Build(layers, testModel(), DefaultParams(8)); len(d.Profile) != 0 {
		t.Fatal("no tower expected")
	}
}
