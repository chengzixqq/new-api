package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func strPtr(s string) *string { return &s }
func fPtr(f float64) *float64  { return &f }

func TestModelGroupPricingModeOnlyNotBareNumberedOrEmpty(t *testing.T) {
	// A group that pins only a billing mode (no price) must survive round-trip:
	// not serialized to a bare number, not judged empty.
	item := ModelGroupPricing{
		Ratio:       fPtr(1.5),
		BillingMode: strPtr(GroupBillingModePerRequest),
	}
	require.False(t, item.IsEmpty())
	require.True(t, item.HasBillingMode())

	raw, err := json.Marshal(item)
	require.NoError(t, err)
	// Must be an object (carries mode), NOT the bare number "1.5".
	require.Equal(t, byte('{'), raw[0])

	var back ModelGroupPricing
	require.NoError(t, json.Unmarshal(raw, &back))
	require.NotNil(t, back.BillingMode)
	require.Equal(t, GroupBillingModePerRequest, *back.BillingMode)
	require.NotNil(t, back.Ratio)
	require.Equal(t, 1.5, *back.Ratio)
}

func TestModelGroupPricingTieredExprRoundTrip(t *testing.T) {
	item := ModelGroupPricing{
		BillingMode: strPtr(GroupBillingModeTieredExpr),
		BillingExpr: strPtr(`tier("base", p * 2)`),
	}
	raw, err := json.Marshal(item)
	require.NoError(t, err)

	var back ModelGroupPricing
	require.NoError(t, json.Unmarshal(raw, &back))
	require.NotNil(t, back.BillingMode)
	require.Equal(t, GroupBillingModeTieredExpr, *back.BillingMode)
	require.NotNil(t, back.BillingExpr)
	require.Equal(t, `tier("base", p * 2)`, *back.BillingExpr)
}

func TestModelGroupPricingBareRatioStillCompact(t *testing.T) {
	// Legacy behavior preserved: ratio-only (no mode, no price) marshals to a bare number.
	item := ModelGroupPricing{Ratio: fPtr(1.25)}
	raw, err := json.Marshal(item)
	require.NoError(t, err)
	require.Equal(t, "1.25", string(raw))
}

func TestModelGroupPricingEmptyIsEmpty(t *testing.T) {
	require.True(t, ModelGroupPricing{}.IsEmpty())
}
