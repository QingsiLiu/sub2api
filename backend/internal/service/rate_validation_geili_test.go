//go:build unit

package service

import (
	"context"
	"math"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestBalanceRateGeiliZeroAndInvalidValues(t *testing.T) {
	for _, rate := range []float64{0, 0.001, 1, 10} {
		require.NoError(t, validateBalanceRateGeili(rate))
	}
	for _, rate := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		require.Equal(t, 400, infraerrors.Code(validateBalanceRateGeili(rate)))
	}
}

func TestUpdateGroupGeiliAcceptsExplicitFreeBalanceRate(t *testing.T) {
	repo := &groupPlatformRepoStub{group: &Group{ID: 1, Name: "free rate fixture", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1}}
	s := &adminServiceImpl{groupRepo: repo}
	zero := 0.0
	group, err := s.UpdateGroup(context.Background(), 1, &UpdateGroupInput{RateMultiplier: &zero})
	require.NoError(t, err)
	require.Zero(t, group.RateMultiplier)
	require.NotNil(t, repo.updated)
	require.Zero(t, repo.updated.RateMultiplier)
}
