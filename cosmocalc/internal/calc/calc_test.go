package calc

import (
	"math"
	"testing"
)

const tol = 1e-9

func almostEqual(a, b float64) bool {
	if b == 0 {
		return math.Abs(a) < tol
	}
	return math.Abs(a-b)/math.Abs(b) < 1e-9
}

// Key acceptance criterion: doubling the observed wavelength (rest fixed)
// must give z' = 2z + 1, not 2z. This pins down z = obs/rest - 1.
func TestRedshiftDefinition_DoublingObservedWavelength(t *testing.T) {
	rest := 500.0
	obs := 600.0
	z1, err := RedshiftFromWavelengths(rest, obs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !almostEqual(z1, 0.2) {
		t.Fatalf("z = %v, want 0.2", z1)
	}
	z2, err := RedshiftFromWavelengths(rest, 2*obs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := 2*z1 + 1
	if !almostEqual(z2, want) {
		t.Fatalf("doubling observed wavelength: z' = %v, want 2z+1 = %v (not 2z = %v)", z2, want, 2*z1)
	}
}

// The rest wavelength must be the denominator: 600/500-1 = 0.2, while
// mistakenly dividing by the observed value would give 1/6.
func TestRedshiftDefinition_RestIsDenominator(t *testing.T) {
	z, err := RedshiftFromWavelengths(500, 600)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !almostEqual(z, 0.2) {
		t.Fatalf("z = %v, want 0.2 (rest wavelength as denominator)", z)
	}
}

// Doubling H0 at fixed redshift must halve the comoving distance.
func TestDistance_InverselyProportionalToHubble(t *testing.T) {
	z := 0.05
	v, err := VelocityFromRedshift(z, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	d1, err := DistanceFromVelocity(v, 70)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	d2, err := DistanceFromVelocity(v, 140)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !almostEqual(d2, d1/2) {
		t.Fatalf("doubling H0: D = %v, want half of %v", d2, d1)
	}
}

// Zero redshift must give exactly zero velocity and zero distance.
func TestZeroRedshift_ZeroVelocityZeroDistance(t *testing.T) {
	v, err := VelocityFromRedshift(0, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 0 {
		t.Fatalf("velocity at z=0: got %v, want exactly 0", v)
	}
	vr, err := VelocityFromRedshift(0, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vr != 0 {
		t.Fatalf("relativistic velocity at z=0: got %v, want exactly 0", vr)
	}
	d, err := DistanceFromVelocity(v, 70)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != 0 {
		t.Fatalf("distance at z=0: got %v, want exactly 0", d)
	}
}

// The linear threshold flags z > 0.1 as beyond the linear regime.
func TestLinearThresholdFlag(t *testing.T) {
	if BeyondLinearThreshold(0.1) {
		t.Fatal("z = 0.1 must still count as linear regime")
	}
	if !BeyondLinearThreshold(0.1001) {
		t.Fatal("z = 0.1001 must be flagged beyond the linear regime")
	}
	if !BeyondLinearThreshold(0.5) {
		t.Fatal("z = 0.5 must be flagged beyond the linear regime")
	}
}

// Blueshift: observed < rest gives z < 0 and must classify as blueshift.
func TestBlueshiftClassification(t *testing.T) {
	z, err := RedshiftFromWavelengths(600, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if z >= 0 {
		t.Fatalf("blueshift z = %v, want negative", z)
	}
	if ClassifyShift(z) != ShiftBlueshift {
		t.Fatalf("classify(%v) = %v, want blueshift", z, ClassifyShift(z))
	}
	if ClassifyShift(0.03) != ShiftRedshift {
		t.Fatal("positive z must classify as redshift")
	}
	if ClassifyShift(0) != ShiftRest {
		t.Fatal("zero z must classify as rest")
	}
}

// The two relations are distinct: at z=1 the linear relation gives v=c
// while the relativistic Doppler relation gives v=0.6c. Neither may be
// silently substituted for the other.
func TestRelativisticAndLinearRelationsAreDistinct(t *testing.T) {
	vLin, err := VelocityFromRedshift(1, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !almostEqual(vLin, SpeedOfLightKmS) {
		t.Fatalf("linear v(z=1) = %v, want c = %v", vLin, SpeedOfLightKmS)
	}
	vRel, err := VelocityFromRedshift(1, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !almostEqual(vRel, 0.6*SpeedOfLightKmS) {
		t.Fatalf("relativistic v(z=1) = %v, want 0.6c = %v", vRel, 0.6*SpeedOfLightKmS)
	}
	if almostEqual(vLin, vRel) {
		t.Fatal("linear and relativistic relations must not coincide at z=1")
	}
}

// Round-trip: velocity -> redshift -> velocity under each relation.
func TestVelocityRedshiftRoundTrip(t *testing.T) {
	for _, rel := range []bool{false, true} {
		z0 := 0.07
		v, err := VelocityFromRedshift(z0, rel)
		if err != nil {
			t.Fatalf("rel=%v: %v", rel, err)
		}
		z1, err := RedshiftFromVelocity(v, rel)
		if err != nil {
			t.Fatalf("rel=%v: %v", rel, err)
		}
		if !almostEqual(z1, z0) {
			t.Fatalf("rel=%v: round trip z = %v, want %v", rel, z1, z0)
		}
	}
}

// Relativistic mode must reject superluminal velocities.
func TestRelativisticRejectsSuperluminal(t *testing.T) {
	_, err := RedshiftFromVelocity(SpeedOfLightKmS*1.5, true)
	ce, ok := err.(*Error)
	if !ok || ce.Kind != ErrVelocityOutOfRange {
		t.Fatalf("superluminal relativistic input: err = %v, want velocity_out_of_range", err)
	}
	// The linear relation does not impose that bound.
	if _, err := RedshiftFromVelocity(SpeedOfLightKmS*1.5, false); err != nil {
		t.Fatalf("linear mode must accept any finite velocity: %v", err)
	}
}

// Non-positive wavelengths and Hubble constants are rejected before any
// arithmetic happens.
func TestNonPositiveInputsRejected(t *testing.T) {
	cases := []struct {
		name string
		fn   func() error
		kind ErrorKind
	}{
		{"zero rest", func() error { _, e := RedshiftFromWavelengths(0, 600); return e }, ErrNonPositiveWavelength},
		{"negative rest", func() error { _, e := RedshiftFromWavelengths(-5, 600); return e }, ErrNonPositiveWavelength},
		{"zero observed", func() error { _, e := RedshiftFromWavelengths(500, 0); return e }, ErrNonPositiveWavelength},
		{"negative observed", func() error { _, e := RedshiftFromWavelengths(500, -1); return e }, ErrNonPositiveWavelength},
		{"zero hubble", func() error { _, e := DistanceFromVelocity(1000, 0); return e }, ErrNonPositiveHubble},
		{"negative hubble", func() error { _, e := DistanceFromVelocity(1000, -70); return e }, ErrNonPositiveHubble},
	}
	for _, c := range cases {
		err := c.fn()
		ce, ok := err.(*Error)
		if !ok || ce.Kind != c.kind {
			t.Errorf("%s: err = %v, want kind %v", c.name, err, c.kind)
		}
	}
}

// Preset worked example: H-alpha 656.28 nm observed at 675.9684 nm
// (z = 0.03) with H0 = 70 km/s/Mpc must land near v = 8993.77 km/s and
// D = 128.48 Mpc.
func TestPresetExampleMatchesHandCalculation(t *testing.T) {
	z, err := RedshiftFromWavelengths(656.28, 656.28*1.03)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !almostEqual(z, 0.03) {
		t.Fatalf("z = %v, want 0.03", z)
	}
	v, err := VelocityFromRedshift(z, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(v-8993.77374) > 0.01 {
		t.Fatalf("v = %v km/s, want ~8993.77 km/s", v)
	}
	d, err := DistanceFromVelocity(v, 70)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(d-128.482) > 0.01 {
		t.Fatalf("D = %v Mpc, want ~128.48 Mpc", d)
	}
}
