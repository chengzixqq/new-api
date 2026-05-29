package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func vStrPtr(s string) *string { return &s }

func TestValidateGroupBillingModesRejectsBadMode(t *testing.T) {
	err := validateGroupBillingModes(map[string]types.ModelGroupPricing{
		"c": {BillingMode: vStrPtr("nonsense")},
	})
	require.Error(t, err)
}

func TestValidateGroupBillingModesRejectsTieredWithoutExpr(t *testing.T) {
	err := validateGroupBillingModes(map[string]types.ModelGroupPricing{
		"c": {BillingMode: vStrPtr(types.GroupBillingModeTieredExpr)},
	})
	require.Error(t, err)
}

func TestValidateGroupBillingModesRejectsBadExpr(t *testing.T) {
	err := validateGroupBillingModes(map[string]types.ModelGroupPricing{
		"c": {
			BillingMode: vStrPtr(types.GroupBillingModeTieredExpr),
			BillingExpr: vStrPtr("this is not a valid expr ((("),
		},
	})
	require.Error(t, err)
}

func TestValidateGroupBillingModesAcceptsValid(t *testing.T) {
	require.NoError(t, validateGroupBillingModes(map[string]types.ModelGroupPricing{
		"c": {BillingMode: vStrPtr(types.GroupBillingModePerRequest)},
		"b": {
			BillingMode: vStrPtr(types.GroupBillingModeTieredExpr),
			BillingExpr: vStrPtr(`tier("base", p * 2)`),
		},
		"default": {Ratio: func() *float64 { f := 1.0; return &f }()},
	}))
}
