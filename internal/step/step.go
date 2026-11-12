// Package step writes a solid of revolution (stacked cylinders and cones about Z)
// as an AP242 STEP file.
package step

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"buddy-tower/internal/tower"
)

type writer struct {
	w  *bufio.Writer
	id int
}

func (s *writer) add(format string, a ...any) int {
	s.id++
	fmt.Fprintf(s.w, "#%d=", s.id)
	fmt.Fprintf(s.w, format, a...)
	s.w.WriteString(";\n")
	return s.id
}

func num(v float64) string {
	if math.Abs(v) < 1e-12 {
		v = 0
	}
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += "."
	}
	return s
}

func pt(x, y, z float64) string {
	return fmt.Sprintf("CARTESIAN_POINT('',(%s,%s,%s))", num(x), num(y), num(z))
}

// Write emits a closed solid whose outline is the profile revolved about the Z axis.
// The profile must run bottom to top with strictly increasing Z and positive radii.
func Write(out io.Writer, name string, profile []tower.Point) error {
	prof := cleanProfile(profile)
	if len(prof) < 2 {
		return fmt.Errorf("profile needs at least two points")
	}
	s := &writer{w: bufio.NewWriter(out)}
	ts := time.Now().Format("2006-01-02T15:04:05")
	fmt.Fprintf(s.w, "ISO-10303-21;\nHEADER;\nFILE_DESCRIPTION(('Buddy Tower'),'2;1');\n")
	fmt.Fprintf(s.w, "FILE_NAME('%s.step','%s',(''),(''),'Buddy Tower','Buddy Tower','');\n", name, ts)
	fmt.Fprintf(s.w, "FILE_SCHEMA(('AP242_MANAGED_MODEL_BASED_3D_ENGINEERING_MIM_LF { 1 0 10303 442 1 1 4 }'));\nENDSEC;\nDATA;\n")

	appCtx := s.add("APPLICATION_CONTEXT('managed model based 3d engineering')")
	s.add("APPLICATION_PROTOCOL_DEFINITION('international standard','ap242_managed_model_based_3d_engineering',2014,#%d)", appCtx)
	prodCtx := s.add("PRODUCT_CONTEXT('',#%d,'mechanical')", appCtx)
	pdCtx := s.add("PRODUCT_DEFINITION_CONTEXT('part definition',#%d,'design')", appCtx)
	lenU := s.add("(LENGTH_UNIT()NAMED_UNIT(*)SI_UNIT(.MILLI.,.METRE.))")
	angU := s.add("(NAMED_UNIT(*)PLANE_ANGLE_UNIT()SI_UNIT($,.RADIAN.))")
	solU := s.add("(NAMED_UNIT(*)SI_UNIT($,.STERADIAN.)SOLID_ANGLE_UNIT())")
	unc := s.add("UNCERTAINTY_MEASURE_WITH_UNIT(LENGTH_MEASURE(1.E-06),#%d,'distance_accuracy_value','confusion accuracy')", lenU)
	geoCtx := s.add("(GEOMETRIC_REPRESENTATION_CONTEXT(3)GLOBAL_UNCERTAINTY_ASSIGNED_CONTEXT((#%d))GLOBAL_UNIT_ASSIGNED_CONTEXT((#%d,#%d,#%d))REPRESENTATION_CONTEXT('Context #1','3D Context with UNIT and UNCERTAINTY'))", unc, lenU, angU, solU)
	origin := s.add("%s", pt(0, 0, 0))
	dirZ := s.add("DIRECTION('',(0.,0.,1.))")
	dirNZ := s.add("DIRECTION('',(0.,0.,-1.))")
	dirX := s.add("DIRECTION('',(1.,0.,0.))")
	worldAx := s.add("AXIS2_PLACEMENT_3D('',#%d,#%d,#%d)", origin, dirZ, dirX)
	prod := s.add("PRODUCT('%s','%s','',(#%d))", name, name, prodCtx)
	pdf := s.add("PRODUCT_DEFINITION_FORMATION('','',#%d)", prod)
	pd := s.add("PRODUCT_DEFINITION('design','',#%d,#%d)", pdf, pdCtx)
	pds := s.add("PRODUCT_DEFINITION_SHAPE('','',#%d)", pd)
	shapeRep := s.add("SHAPE_REPRESENTATION('%s',(#%d),#%d)", name, worldAx, geoCtx)
	s.add("SHAPE_DEFINITION_REPRESENTATION(#%d,#%d)", pds, shapeRep)
	s.add("PRODUCT_RELATED_PRODUCT_CATEGORY('part',$,(#%d))", prod)

	n := len(prof)
	// one circle edge (with a single vertex on +X) per profile point
	verts := make([]int, n)
	circles := make([]int, n)
	for i, p := range prof {
		c := s.add("%s", pt(0, 0, p.Z))
		ax := s.add("AXIS2_PLACEMENT_3D('',#%d,#%d,#%d)", c, dirZ, dirX)
		circ := s.add("CIRCLE('',#%d,%s)", ax, num(p.R))
		vp := s.add("%s", pt(p.R, 0, p.Z))
		verts[i] = s.add("VERTEX_POINT('',#%d)", vp)
		circles[i] = s.add("EDGE_CURVE('',#%d,#%d,#%d,.T.)", verts[i], verts[i], circ)
	}
	var faces []int
	// bottom cap, normal -Z: circle runs clockwise seen from below, so reversed
	{
		ax := s.add("AXIS2_PLACEMENT_3D('',#%d,#%d,#%d)", s.add("%s", pt(0, 0, prof[0].Z)), dirNZ, dirX)
		pl := s.add("PLANE('',#%d)", ax)
		oe := s.add("ORIENTED_EDGE('',*,*,#%d,.F.)", circles[0])
		loop := s.add("EDGE_LOOP('',(#%d))", oe)
		fb := s.add("FACE_BOUND('',#%d,.T.)", loop)
		faces = append(faces, s.add("ADVANCED_FACE('',(#%d),#%d,.T.)", fb, pl))
	}
	// side faces
	for i := 1; i < n; i++ {
		a, b := prof[i-1], prof[i]
		h := b.Z - a.Z
		// seam line from bottom vertex up to top vertex
		lp := s.add("%s", pt(a.R, 0, a.Z))
		dx, dz := b.R-a.R, h
		l := math.Hypot(dx, dz)
		dir := s.add("DIRECTION('',(%s,0.,%s))", num(dx/l), num(dz/l))
		vec := s.add("VECTOR('',#%d,%s)", dir, num(l))
		line := s.add("LINE('',#%d,#%d)", lp, vec)
		seam := s.add("EDGE_CURVE('',#%d,#%d,#%d,.T.)", verts[i-1], verts[i], line)

		var surf int
		switch {
		case math.Abs(b.R-a.R) < 1e-9:
			ax := s.add("AXIS2_PLACEMENT_3D('',#%d,#%d,#%d)", s.add("%s", pt(0, 0, a.Z)), dirZ, dirX)
			surf = s.add("CYLINDRICAL_SURFACE('',#%d,%s)", ax, num(a.R))
		case b.R > a.R:
			ax := s.add("AXIS2_PLACEMENT_3D('',#%d,#%d,#%d)", s.add("%s", pt(0, 0, a.Z)), dirZ, dirX)
			surf = s.add("CONICAL_SURFACE('',#%d,%s,%s)", ax, num(a.R), num(math.Atan((b.R-a.R)/h)))
		default:
			// narrowing: flip the axis so the semi-angle stays positive
			ax := s.add("AXIS2_PLACEMENT_3D('',#%d,#%d,#%d)", s.add("%s", pt(0, 0, b.Z)), dirNZ, dirX)
			surf = s.add("CONICAL_SURFACE('',#%d,%s,%s)", ax, num(b.R), num(math.Atan((a.R-b.R)/h)))
		}
		e1 := s.add("ORIENTED_EDGE('',*,*,#%d,.T.)", circles[i-1])
		e2 := s.add("ORIENTED_EDGE('',*,*,#%d,.T.)", seam)
		e3 := s.add("ORIENTED_EDGE('',*,*,#%d,.F.)", circles[i])
		e4 := s.add("ORIENTED_EDGE('',*,*,#%d,.F.)", seam)
		loop := s.add("EDGE_LOOP('',(#%d,#%d,#%d,#%d))", e1, e2, e3, e4)
		fb := s.add("FACE_BOUND('',#%d,.T.)", loop)
		faces = append(faces, s.add("ADVANCED_FACE('',(#%d),#%d,.T.)", fb, surf))
	}
	// top cap, normal +Z
	{
		ax := s.add("AXIS2_PLACEMENT_3D('',#%d,#%d,#%d)", s.add("%s", pt(0, 0, prof[n-1].Z)), dirZ, dirX)
		pl := s.add("PLANE('',#%d)", ax)
		oe := s.add("ORIENTED_EDGE('',*,*,#%d,.T.)", circles[n-1])
		loop := s.add("EDGE_LOOP('',(#%d))", oe)
		fb := s.add("FACE_BOUND('',#%d,.T.)", loop)
		faces = append(faces, s.add("ADVANCED_FACE('',(#%d),#%d,.T.)", fb, pl))
	}
	refs := make([]string, len(faces))
	for i, f := range faces {
		refs[i] = "#" + strconv.Itoa(f)
	}
	shell := s.add("CLOSED_SHELL('',(%s))", strings.Join(refs, ","))
	brep := s.add("MANIFOLD_SOLID_BREP('%s',#%d)", name, shell)
	brepRep := s.add("ADVANCED_BREP_SHAPE_REPRESENTATION('%s',(#%d,#%d),#%d)", name, brep, worldAx, geoCtx)
	s.add("SHAPE_REPRESENTATION_RELATIONSHIP('','',#%d,#%d)", shapeRep, brepRep)
	s.w.WriteString("ENDSEC;\nEND-ISO-10303-21;\n")
	return s.w.Flush()
}

// cleanProfile drops degenerate points and merges collinear runs.
func cleanProfile(in []tower.Point) []tower.Point {
	var out []tower.Point
	for _, p := range in {
		if p.R <= 0 {
			continue
		}
		if len(out) > 0 && p.Z-out[len(out)-1].Z < 1e-6 {
			continue
		}
		// merge if collinear with the previous two points
		if len(out) >= 2 {
			a, b := out[len(out)-2], out[len(out)-1]
			s1 := (b.R - a.R) / (b.Z - a.Z)
			s2 := (p.R - b.R) / (p.Z - b.Z)
			if math.Abs(s1-s2) < 1e-9 {
				out[len(out)-1] = p
				continue
			}
		}
		out = append(out, p)
	}
	return out
}
