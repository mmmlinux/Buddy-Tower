// Package threemf finds and opens the gcode files stored inside a .3mf archive.
package threemf

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Entry describes one gcode file inside the archive.
type Entry struct {
	Name string // path inside the zip, e.g. Metadata/plate_1.gcode
	Size int64  // uncompressed size in bytes
}

// Label is a friendly name for the entry ("plate_1").
func (e Entry) Label() string {
	return strings.TrimSuffix(path.Base(e.Name), path.Ext(e.Name))
}

// ListGcodes returns every .gcode entry in the archive, sorted naturally.
func ListGcodes(file string) ([]Entry, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, fmt.Errorf("open 3mf: %w", err)
	}
	defer zr.Close()
	var out []Entry
	for _, f := range zr.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".gcode") {
			out = append(out, Entry{Name: f.Name, Size: int64(f.UncompressedSize64)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return naturalLess(out[i].Name, out[j].Name) })
	return out, nil
}

// ReadCloser streams a gcode entry and closes the underlying archive on Close.
type ReadCloser struct {
	io.Reader
	entry io.ReadCloser
	zr    *zip.ReadCloser
	Size  int64
}

// Close releases the entry and the archive.
func (r *ReadCloser) Close() error {
	r.entry.Close()
	return r.zr.Close()
}

// Open opens the archive fresh from disk and returns a stream of the named entry.
func Open(file, name string) (*ReadCloser, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, fmt.Errorf("open 3mf: %w", err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				zr.Close()
				return nil, fmt.Errorf("open %s: %w", name, err)
			}
			return &ReadCloser{Reader: rc, entry: rc, zr: zr, Size: int64(f.UncompressedSize64)}, nil
		}
	}
	zr.Close()
	return nil, fmt.Errorf("%s not found in archive (was the file changed?)", name)
}

// naturalLess compares strings so that plate_2 sorts before plate_10.
func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		ca, cb := a[0], b[0]
		if isDigit(ca) && isDigit(cb) {
			na, ra := leadingNum(a)
			nb, rb := leadingNum(b)
			if na != nb {
				return na < nb
			}
			a, b = ra, rb
			continue
		}
		if ca != cb {
			return ca < cb
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func leadingNum(s string) (int, string) {
	n, i := 0, 0
	for i < len(s) && isDigit(s[i]) {
		n = n*10 + int(s[i]-'0')
		i++
	}
	return n, s[i:]
}

// TowerPath is the default output path for a tower: next to the 3MF, named after it,
// with the plate added when the archive holds more than one gcode.
func TowerPath(file string, e Entry, multi bool) string {
	base := path.Base(strings.ReplaceAll(file, "\\", "/"))
	lower := strings.ToLower(base)
	for _, suf := range []string{".gcode.3mf", ".3mf"} {
		if strings.HasSuffix(lower, suf) {
			base = base[:len(base)-len(suf)]
			break
		}
	}
	if multi {
		base += "_" + e.Label()
	}
	return filepath.Join(filepath.Dir(file), base+"_tower.step")
}
