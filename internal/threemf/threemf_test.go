package threemf

import (
	"path/filepath"
	"testing"
)

func TestTowerPath(t *testing.T) {
	e := Entry{Name: "Metadata/plate_2.gcode"}
	cases := []struct {
		file  string
		multi bool
		want  string
	}{
		{"examplegcode.gcode.3mf", false, "examplegcode_tower.step"},
		{"/a/b/Part.GCODE.3MF", false, "/a/b/Part_tower.step"},
		{"/a/b/thing.3mf", true, "/a/b/thing_plate_2_tower.step"},
	}
	for _, c := range cases {
		if got := TowerPath(c.file, e, c.multi); got != filepath.FromSlash(c.want) {
			t.Errorf("TowerPath(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}
