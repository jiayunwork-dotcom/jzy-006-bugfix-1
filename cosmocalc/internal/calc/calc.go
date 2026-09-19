// Package calc implements the pure numerical core of the cosmological
// redshift / Hubble-distance service. It has no I/O and no dependencies;
// every function is safe for concurrent use.
//
// Fixed unit conventions (never mixed):
//   - distances:       megaparsecs (Mpc)
//   - velocities:      kilometres per second (km/s)
//   - Hubble constant: km/s per Mpc (km s^-1 Mpc^-1)
//   - wavelengths:     any unit, as long as rest and observed match
//     (only their ratio enters the redshift definition)
package calc

import (
	"fmt"
	"math"
)

// SpeedOfLightKmS is the exact speed of light in km/s.
const SpeedOfLightKmS = 299792.458

// DefaultLinearThreshold is the fixed redshift limit of the low-redshift
// linear regime. Above it the linear Hubble approximation is still
// reported but must be flagged as unreliable.
const DefaultLinearThreshold = 0.1

// ErrorKind classifies calculation errors so the API layer can map them
// to distinct structured error responses.
type ErrorKind string

const (
	ErrNonPositiveWavelength ErrorKind = "non_positive_wavelength"
	ErrNonPositiveHubble     ErrorKind = "non_positive_hubble_constant"
	ErrRedshiftOutOfRange    ErrorKind = "redshift_out_of_range"
	ErrVelocityOutOfRange    ErrorKind = "velocity_out_of_range"
	ErrBlueshiftDistance     ErrorKind = "blueshift_distance"
)

// Error is a typed calculation error.
type Error struct {
	Kind    ErrorKind
	Field   string
	Message string
}

func (e *Error) Error() string { return e.Message }

// ShiftType labels the direction of a spectral shift.
type ShiftType string

const (
	ShiftRedshift  ShiftType = "redshift"
	ShiftBlueshift ShiftType = "blueshift"
	ShiftRest      ShiftType = "rest"
)

// Relation identifies which velocity<->redshift relation was used.
type Relation string

const (
	// RelationLinear is the default cosmological low-z relation v = c*z.
	RelationLinear Relation = "cosmological_linear"
	// RelationRelativistic is the optional special-relativistic Doppler relation.
	RelationRelativistic Relation = "relativistic_doppler"
)

// RedshiftFromWavelengths computes z = observed/rest - 1.
//
// The rest wavelength is the denominator; observed > rest gives a
// positive redshift, observed < rest a negative one (blueshift).
func RedshiftFromWavelengths(rest, observed float64) (float64, error) {
	if rest <= 0 {
		return 0, &Error{Kind: ErrNonPositiveWavelength, Field: "rest_wavelength",
			Message: fmt.Sprintf("rest wavelength must be positive, got %v", rest)}
	}
	if observed <= 0 {
		return 0, &Error{Kind: ErrNonPositiveWavelength, Field: "observed_wavelength",
			Message: fmt.Sprintf("observed wavelength must be positive, got %v", observed)}
	}
	return observed/rest - 1, nil
}

// ValidateRedshift rejects physically impossible redshifts (z <= -1 would
// require a non-positive observed wavelength or |v| >= c).
func ValidateRedshift(z float64) error {
	if math.IsNaN(z) || math.IsInf(z, 0) {
		return &Error{Kind: ErrRedshiftOutOfRange, Field: "redshift",
			Message: fmt.Sprintf("redshift must be finite, got %v", z)}
	}
	if z <= -1 {
		return &Error{Kind: ErrRedshiftOutOfRange, Field: "redshift",
			Message: fmt.Sprintf("redshift must be greater than -1, got %v", z)}
	}
	return nil
}

// VelocityFromRedshift converts a redshift to a recession velocity in km/s.
//
// Default (relativistic=false): cosmological linear relation v = c*z.
// Optional (relativistic=true): special-relativistic Doppler relation
// beta = ((1+z)^2 - 1) / ((1+z)^2 + 1), v = beta*c.
// The two relations are deliberately separate code paths selected by the
// caller; they are never blended into a single formula.
func VelocityFromRedshift(z float64, relativistic bool) (float64, error) {
	if err := ValidateRedshift(z); err != nil {
		return 0, err
	}
	if !relativistic {
		return SpeedOfLightKmS * z, nil
	}
	s := (1 + z) * (1 + z)
	beta := (s - 1) / (s + 1)
	return SpeedOfLightKmS * beta, nil
}

// RedshiftFromVelocity converts a recession velocity in km/s to a redshift,
// using the relation selected by the relativistic flag (see
// VelocityFromRedshift). In the relativistic mode |v| must be below c.
func RedshiftFromVelocity(v float64, relativistic bool) (float64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, &Error{Kind: ErrVelocityOutOfRange, Field: "velocity_kms",
			Message: fmt.Sprintf("velocity must be finite, got %v", v)}
	}
	if !relativistic {
		return v / SpeedOfLightKmS, nil
	}
	beta := v / SpeedOfLightKmS
	if math.Abs(beta) >= 1 {
		return 0, &Error{Kind: ErrVelocityOutOfRange, Field: "velocity_kms",
			Message: fmt.Sprintf("relativistic Doppler relation requires |v| < c (%v km/s), got %v km/s", SpeedOfLightKmS, v)}
	}
	return math.Sqrt((1+beta)/(1-beta)) - 1, nil
}

// DistanceFromVelocity applies the Hubble law D = v / H0.
// v in km/s, h0 in km/s/Mpc, result in Mpc.
func DistanceFromVelocity(v, h0 float64) (float64, error) {
	if h0 <= 0 || math.IsNaN(h0) || math.IsInf(h0, 0) {
		return 0, &Error{Kind: ErrNonPositiveHubble, Field: "hubble_constant",
			Message: fmt.Sprintf("Hubble constant must be positive (km/s/Mpc), got %v", h0)}
	}
	return v / h0, nil
}

// ClassifyShift labels a redshift: positive = redshift, negative =
// blueshift, exactly zero = rest (no recession).
func ClassifyShift(z float64) ShiftType {
	switch {
	case z > 0:
		return ShiftRedshift
	case z < 0:
		return ShiftBlueshift
	default:
		return ShiftRest
	}
}

// BeyondLinearThreshold reports whether z exceeds the fixed linear
// threshold, i.e. whether the low-z linear approximation is unreliable.
func BeyondLinearThreshold(z float64) bool {
	return z > DefaultLinearThreshold
}
