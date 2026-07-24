package model

import (
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertEpayTopUpForTest(t *testing.T, tradeNo string, userID int, money float64) {
	t.Helper()
	require.NoError(t, (&TopUp{
		UserId:          userID,
		Amount:          2,
		Money:           money,
		TradeNo:         tradeNo,
		PaymentMethod:   "alipay",
		PaymentProvider: PaymentProviderEpay,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}).Insert())
}

func TestCompleteEpayTopUpCommitsOrderAndQuotaAtomically(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 501, 100)
	insertEpayTopUpForTest(t, "epay-atomic-success", 501, 9.99)

	completion, err := CompleteEpayTopUp("epay-atomic-success", "alipay", "9.99")
	require.NoError(t, err)
	assert.False(t, completion.AlreadyCompleted)
	assert.Equal(t, common.QuotaFromFloat(2*common.QuotaPerUnit), completion.QuotaAdded)

	topUp := GetTopUpByTradeNo("epay-atomic-success")
	require.NotNil(t, topUp)
	assert.Equal(t, common.TopUpStatusSuccess, topUp.Status)
	assert.NotZero(t, topUp.CompleteTime)
	assert.Equal(t, 100+completion.QuotaAdded, getUserQuotaForPaymentGuardTest(t, 501))
}

func TestCompleteEpayTopUpIsIdempotentForMatchingReplay(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 502, 0)
	insertEpayTopUpForTest(t, "epay-idempotent", 502, 8.50)

	first, err := CompleteEpayTopUp("epay-idempotent", "alipay", "8.50")
	require.NoError(t, err)
	second, err := CompleteEpayTopUp("epay-idempotent", "alipay", "8.500")
	require.NoError(t, err)

	assert.False(t, first.AlreadyCompleted)
	assert.True(t, second.AlreadyCompleted)
	assert.Equal(t, first.QuotaAdded, getUserQuotaForPaymentGuardTest(t, 502))
}

func TestCompleteEpayTopUpRejectsMismatchedCallbackFacts(t *testing.T) {
	testCases := []struct {
		name          string
		paymentMethod string
		paidMoney     string
		expectedError error
	}{
		{name: "payment method", paymentMethod: "wxpay", paidMoney: "9.99", expectedError: ErrPaymentMethodMismatch},
		{name: "amount", paymentMethod: "alipay", paidMoney: "9.98", expectedError: ErrPaymentAmountMismatch},
		{name: "invalid amount", paymentMethod: "alipay", paidMoney: "not-a-number", expectedError: ErrPaymentAmountMismatch},
	}

	for index, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			truncateTables(t)
			userID := 510 + index
			tradeNo := "epay-mismatch-" + testCase.name
			insertUserForPaymentGuardTest(t, userID, 0)
			insertEpayTopUpForTest(t, tradeNo, userID, 9.99)

			_, err := CompleteEpayTopUp(tradeNo, testCase.paymentMethod, testCase.paidMoney)
			require.ErrorIs(t, err, testCase.expectedError)
			assert.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, tradeNo))
			assert.Zero(t, getUserQuotaForPaymentGuardTest(t, userID))
		})
	}
}

func TestCompleteEpayTopUpRollsBackOrderWhenUserCreditFails(t *testing.T) {
	truncateTables(t)
	insertEpayTopUpForTest(t, "epay-missing-user", 9999, 9.99)

	_, err := CompleteEpayTopUp("epay-missing-user", "alipay", "9.99")
	require.Error(t, err)
	assert.Equal(t, common.TopUpStatusPending, getTopUpStatusForPaymentGuardTest(t, "epay-missing-user"))
}

func TestCompleteEpayTopUpConcurrentReplayCreditsOnce(t *testing.T) {
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 503, 0)
	insertEpayTopUpForTest(t, "epay-concurrent", 503, 9.99)

	const callbacks = 4
	results := make(chan *EpayTopUpCompletion, callbacks)
	errors := make(chan error, callbacks)
	var waitGroup sync.WaitGroup
	for range callbacks {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			result, err := CompleteEpayTopUp("epay-concurrent", "alipay", "9.99")
			results <- result
			errors <- err
		}()
	}
	waitGroup.Wait()
	close(results)
	close(errors)

	for err := range errors {
		require.NoError(t, err)
	}
	newCompletions := 0
	quotaAdded := 0
	for result := range results {
		require.NotNil(t, result)
		quotaAdded = result.QuotaAdded
		if !result.AlreadyCompleted {
			newCompletions++
		}
	}
	assert.Equal(t, 1, newCompletions)
	assert.Equal(t, quotaAdded, getUserQuotaForPaymentGuardTest(t, 503))
}
