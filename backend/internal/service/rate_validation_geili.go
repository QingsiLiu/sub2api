package service

import (
	"math"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Zero is an explicit free balance rate, not an omitted/default rate.
func validateBalanceRateGeili(rate float64) error {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
		return infraerrors.BadRequest("INVALID_RATE_MULTIPLIER", "rate_multiplier must be finite and >= 0")
	}
	return nil
}
