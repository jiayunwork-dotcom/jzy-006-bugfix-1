// Package service orchestrates the calculation pipeline: it resolves the
// caller's input (wavelengths, redshift, or velocity) into a redshift,
// applies the selected velocity relation, enforces the blueshift and
// linear-regime rules, and returns transport-ready result structs.
package service

import (
	"errors"

	"cosmocalc/internal/calc"
)

// Input carries one observation. Exactly one of the three input modes
// must be supplied:
//   - rest_wavelength + observed_wavelength
//   - redshift
//   - velocity_kms
//
// Relativistic selects the relativistic Doppler relation instead of the
// default cosmological linear relation for velocity<->redshift conversion.
type Input struct {
	RestWavelength     *float64 `json:"rest_wavelength,omitempty"`
	ObservedWavelength *float64 `json:"observed_wavelength,omitempty"`
	Redshift           *float64 `json:"redshift,omitempty"`
	VelocityKmS        *float64 `json:"velocity_kms,omitempty"`
	Relativistic       bool     `json:"relativistic,omitempty"`
}

// DistanceInput extends Input with the Hubble constant required for
// distance/velocity queries.
type DistanceInput struct {
	Input
	HubbleConstant *float64 `json:"hubble_constant,omitempty"`
}

// RedshiftResult is the outcome of a redshift computation.
type RedshiftResult struct {
	Redshift    float64        `json:"redshift"`
	VelocityKmS float64        `json:"velocity_kms"`
	ShiftType   calc.ShiftType `json:"shift_type"`
	Relation    calc.Relation  `json:"relation"`
}

// DistanceResult is the outcome of a distance/velocity computation.
type DistanceResult struct {
	RedshiftResult
	HubbleConstant  float64 `json:"hubble_constant"`
	DistanceMpc     float64 `json:"distance_mpc"`
	LinearRegime    bool    `json:"linear_regime"`
	BeyondLinear    bool    `json:"beyond_linear_threshold"`
	LinearThreshold float64 `json:"linear_threshold"`
	Warning         string  `json:"warning,omitempty"`
}

// MissingFieldError reports an absent required input field.
type MissingFieldError struct {
	Field   string
	Message string
}

func (e *MissingFieldError) Error() string { return e.Message }

// relationOf maps the toggle to its label.
func relationOf(relativistic bool) calc.Relation {
	if relativistic {
		return calc.RelationRelativistic
	}
	return calc.RelationLinear
}

// resolveRedshift turns any of the three input modes into a redshift.
func resolveRedshift(in Input) (float64, error) {
	hasRest := in.RestWavelength != nil
	hasObs := in.ObservedWavelength != nil
	switch {
	case hasRest && hasObs:
		return calc.RedshiftFromWavelengths(*in.RestWavelength, *in.ObservedWavelength)
	case hasRest != hasObs:
		missing := "observed_wavelength"
		if !hasRest {
			missing = "rest_wavelength"
		}
		return 0, &MissingFieldError{Field: missing,
			Message: "wavelength mode requires both rest_wavelength and observed_wavelength; missing " + missing}
	case in.Redshift != nil:
		if err := calc.ValidateRedshift(*in.Redshift); err != nil {
			return 0, err
		}
		return *in.Redshift, nil
	case in.VelocityKmS != nil:
		return calc.RedshiftFromVelocity(*in.VelocityKmS, in.Relativistic)
	default:
		return 0, &MissingFieldError{Field: "input",
			Message: "provide one of: rest_wavelength+observed_wavelength, redshift, or velocity_kms"}
	}
}

// ComputeRedshift resolves the input and returns redshift, recession
// velocity and shift classification. A zero redshift yields exactly zero
// velocity.
func ComputeRedshift(in Input) (*RedshiftResult, error) {
	z, err := resolveRedshift(in)
	if err != nil {
		return nil, err
	}
	v, err := calc.VelocityFromRedshift(z, in.Relativistic)
	if err != nil {
		return nil, err
	}
	return &RedshiftResult{
		Redshift:    z,
		VelocityKmS: v,
		ShiftType:   calc.ClassifyShift(z),
		Relation:    relationOf(in.Relativistic),
	}, nil
}

// ComputeDistance resolves the input, computes recession velocity and
// comoving distance D = v/H0, and flags results beyond the linear regime.
//
// Blueshifted inputs (z < 0) are rejected with a typed blueshift error:
// the service never reports them as a forward positive distance.
func ComputeDistance(in DistanceInput) (*DistanceResult, error) {
	if in.HubbleConstant == nil {
		return nil, &MissingFieldError{Field: "hubble_constant",
			Message: "hubble_constant (km/s/Mpc) is required for distance queries"}
	}
	res, err := ComputeRedshift(in.Input)
	if err != nil {
		return nil, err
	}
	if res.ShiftType == calc.ShiftBlueshift {
		return nil, &calc.Error{Kind: calc.ErrBlueshiftDistance, Field: "redshift",
			Message: "blueshift detected (z < 0): the object is approaching, no Hubble-flow distance is defined"}
	}
	d, err := calc.DistanceFromVelocity(res.VelocityKmS, *in.HubbleConstant)
	if err != nil {
		return nil, err
	}
	out := &DistanceResult{
		RedshiftResult:  *res,
		HubbleConstant:  *in.HubbleConstant,
		DistanceMpc:     d,
		LinearRegime:    !calc.BeyondLinearThreshold(res.Redshift),
		BeyondLinear:    calc.BeyondLinearThreshold(res.Redshift),
		LinearThreshold: calc.DefaultLinearThreshold,
	}
	if out.BeyondLinear {
		out.Warning = "redshift exceeds the linear-regime threshold; linear approximation returned but no longer reliable"
	}
	return out, nil
}

// AsCalcError unwraps err into a *calc.Error if possible.
func AsCalcError(err error) (*calc.Error, bool) {
	var ce *calc.Error
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}

// AsMissingField unwraps err into a *MissingFieldError if possible.
func AsMissingField(err error) (*MissingFieldError, bool) {
	var me *MissingFieldError
	if errors.As(err, &me) {
		return me, true
	}
	return nil, false
}
