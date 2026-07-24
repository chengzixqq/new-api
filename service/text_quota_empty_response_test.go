package service

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type emptyResponseBillingSettler struct {
	preConsumed int
	settled     bool
	failLocked  bool
	settleCalls []int
	refundCalls int
}

func (s *emptyResponseBillingSettler) Settle(actualQuota int) error {
	s.settleCalls = append(s.settleCalls, actualQuota)
	if actualQuota != s.preConsumed {
		return errors.New("actual settlement failed")
	}
	if s.failLocked {
		return errors.New("fallback settlement failed")
	}
	s.settled = true
	return nil
}

func (s *emptyResponseBillingSettler) Refund(_ *gin.Context) {
	if !s.settled {
		s.refundCalls++
	}
}

func (s *emptyResponseBillingSettler) NeedsRefund() bool {
	return !s.settled && s.preConsumed > 0
}

func (s *emptyResponseBillingSettler) IsSettled() bool {
	return s.settled
}

func (s *emptyResponseBillingSettler) GetPreConsumedQuota() int {
	return s.preConsumed
}

func (s *emptyResponseBillingSettler) Reserve(_ int) error {
	return nil
}

func TestUpstreamEmptyResponseMinimumQuota(t *testing.T) {
	info := &relaycommon.RelayInfo{
		FinalPreConsumedQuota: 80,
		PriceData: types.PriceData{
			QuotaToPreConsume: 100,
		},
	}

	require.Equal(t, 100, upstreamEmptyResponseMinimumQuota(info, nil))
	require.Equal(t, 100, upstreamEmptyResponseMinimumQuota(info, &dto.Usage{}))
	require.Equal(t, 100, upstreamEmptyResponseMinimumQuota(info, &dto.Usage{TotalTokens: 30}))
	require.Equal(t, 0, upstreamEmptyResponseMinimumQuota(info, &dto.Usage{CompletionTokens: 1}))

	info.FinalPreConsumedQuota = -10
	info.PriceData.QuotaToPreConsume = -20
	require.Equal(t, 0, upstreamEmptyResponseMinimumQuota(info, nil))
}

func TestRetainPreConsumedQuotaPreventsErrorPathRefund(t *testing.T) {
	billing := &emptyResponseBillingSettler{preConsumed: 80}
	info := &relaycommon.RelayInfo{Billing: billing}

	require.Error(t, billing.Settle(120))
	lockedQuota, retained, err := retainPreConsumedQuotaAfterSettlementFailure(info)
	require.NoError(t, err)
	require.True(t, retained)
	require.Equal(t, 80, lockedQuota)
	require.Equal(t, []int{120, 80}, billing.settleCalls)

	billing.Refund(nil)
	require.Zero(t, billing.refundCalls)
}

func TestRetainPreConsumedQuotaReportsFallbackFailure(t *testing.T) {
	billing := &emptyResponseBillingSettler{preConsumed: 80, failLocked: true}
	info := &relaycommon.RelayInfo{Billing: billing}

	lockedQuota, retained, err := retainPreConsumedQuotaAfterSettlementFailure(info)
	require.Error(t, err)
	require.False(t, retained)
	require.Equal(t, 80, lockedQuota)
	require.Equal(t, []int{80}, billing.settleCalls)
}

func TestZeroPreConsumedQuotaIsNotAConfirmedSettlement(t *testing.T) {
	billing := &emptyResponseBillingSettler{}
	info := &relaycommon.RelayInfo{Billing: billing}

	require.Error(t, billing.Settle(120))
	lockedQuota, retained, err := retainPreConsumedQuotaAfterSettlementFailure(info)
	require.NoError(t, err)
	require.False(t, retained)
	require.Zero(t, lockedQuota)
	require.False(t, billing.IsSettled())
}
