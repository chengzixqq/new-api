package service

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestCalcViolationFeeQuotaUsesSaturatingConversion(t *testing.T) {
	require.Zero(t, calcViolationFeeQuota(0, 1))
	require.Zero(t, calcViolationFeeQuota(1, 0))
	require.Equal(t, 250000, calcViolationFeeQuota(0.5, 1))
	require.Equal(t, common.MaxQuota, calcViolationFeeQuota(math.MaxFloat64, 1))
}
