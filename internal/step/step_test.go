package step

import (
	"bytes"
	"regexp"
	"testing"

	"buddy-tower/internal/tower"
)

// TestClosedShell checks every edge is used exactly twice, once in each direction.
func TestClosedShell(t *testing.T) {
	prof := []tower.Point{{Z: 0, R: 15}, {Z: 0.23, R: 15}, {Z: 6, R: 5}, {Z: 198, R: 5}, {Z: 213, R: 20}, {Z: 228, R: 20}}
	var buf bytes.Buffer
	if err := Write(&buf, "test", prof); err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`ORIENTED_EDGE\('',\*,\*,#(\d+),\.([TF])\.\)`)
	uses := map[string][2]int{}
	for _, m := range re.FindAllStringSubmatch(buf.String(), -1) {
		u := uses[m[1]]
		if m[2] == "T" {
			u[0]++
		} else {
			u[1]++
		}
		uses[m[1]] = u
	}
	edges := regexp.MustCompile(`#(\d+)=EDGE_CURVE`).FindAllStringSubmatch(buf.String(), -1)
	if len(edges) != 2*len(prof)-1 {
		t.Fatalf("got %d edges, want %d", len(edges), 2*len(prof)-1)
	}
	for _, e := range edges {
		if u := uses[e[1]]; u != [2]int{1, 1} {
			t.Errorf("edge #%s used %v (T,F), want exactly once each", e[1], u)
		}
	}
	faces := regexp.MustCompile(`ADVANCED_FACE`).FindAllString(buf.String(), -1)
	if len(faces) != len(prof)+1 {
		t.Errorf("got %d faces, want %d", len(faces), len(prof)+1)
	}
}
