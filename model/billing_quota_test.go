package model

import (
	"errors"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedBillingQuotaRows(t *testing.T, userQuota int, tokenQuota int, unlimited bool) (int, int, string) {
	t.Helper()
	user := &User{
		Username: "billing_quota_user",
		Quota:    userQuota,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)
	token := &Token{
		UserId:         user.Id,
		Key:            "billing-quota-token",
		Name:           "billing quota token",
		Status:         common.TokenStatusEnabled,
		RemainQuota:    tokenQuota,
		UnlimitedQuota: unlimited,
	}
	require.NoError(t, DB.Create(token).Error)
	return user.Id, token.Id, token.Key
}

func readBillingQuotaRows(t *testing.T, userID int, tokenID int) (User, Token) {
	t.Helper()
	var user User
	require.NoError(t, DB.Select("quota").First(&user, userID).Error)
	var token Token
	require.NoError(t, DB.Select("remain_quota", "used_quota").First(&token, tokenID).Error)
	return user, token
}

func TestAdjustWalletAndTokenQuotaRollsBackWhenTokenIsInsufficient(t *testing.T) {
	truncateTables(t)
	userID, tokenID, tokenKey := seedBillingQuotaRows(t, 100, 10, false)

	err := AdjustWalletAndTokenQuota(userID, tokenID, tokenKey, 20, false)

	require.ErrorIs(t, err, ErrInsufficientTokenQuota)
	user, token := readBillingQuotaRows(t, userID, tokenID)
	assert.Equal(t, 100, user.Quota)
	assert.Equal(t, 10, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
}

func TestAdjustWalletAndTokenQuotaAllowsOnlyOneConcurrentSpend(t *testing.T) {
	truncateTables(t)
	userID, tokenID, tokenKey := seedBillingQuotaRows(t, 100, 100, false)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- AdjustWalletAndTokenQuota(userID, tokenID, tokenKey, 60, false)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var successes int
	var insufficient int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrInsufficientUserQuota), errors.Is(err, ErrInsufficientTokenQuota):
			insufficient++
		default:
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, insufficient)
	user, token := readBillingQuotaRows(t, userID, tokenID)
	assert.Equal(t, 40, user.Quota)
	assert.Equal(t, 40, token.RemainQuota)
	assert.Equal(t, 60, token.UsedQuota)
}

func TestUnlimitedTokenTracksUsageWithoutChangingRemainingQuota(t *testing.T) {
	truncateTables(t)
	userID, tokenID, tokenKey := seedBillingQuotaRows(t, 100, 0, true)

	require.NoError(t, AdjustWalletAndTokenQuota(userID, tokenID, tokenKey, 30, false))
	user, token := readBillingQuotaRows(t, userID, tokenID)
	assert.Equal(t, 70, user.Quota)
	assert.Zero(t, token.RemainQuota)
	assert.Equal(t, 30, token.UsedQuota)

	require.NoError(t, AdjustWalletAndTokenQuota(userID, tokenID, tokenKey, -30, false))
	user, token = readBillingQuotaRows(t, userID, tokenID)
	assert.Equal(t, 100, user.Quota)
	assert.Zero(t, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
}

func TestFinancialQuotaUpdatesIgnoreBatchMode(t *testing.T) {
	truncateTables(t)
	userID, tokenID, tokenKey := seedBillingQuotaRows(t, 100, 100, false)
	previousBatchMode := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() {
		common.BatchUpdateEnabled = previousBatchMode
	})

	require.NoError(t, DecreaseUserQuota(userID, 25, false))
	require.NoError(t, DecreaseTokenQuota(tokenID, tokenKey, 25))

	user, token := readBillingQuotaRows(t, userID, tokenID)
	assert.Equal(t, 75, user.Quota)
	assert.Equal(t, 75, token.RemainQuota)
	assert.Equal(t, 25, token.UsedQuota)
}
