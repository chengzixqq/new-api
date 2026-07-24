package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/stretchr/testify/require"
)

func TestBillingSessionReserveRejectsRepriceAboveWalletBalanceAtomically(t *testing.T) {
	truncate(t)
	seedUser(t, 7, 100)
	seedToken(t, 8, 7, "atomic-reserve-token", 1000)

	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{
			UserId:   7,
			TokenId:  8,
			TokenKey: "atomic-reserve-token",
		},
		funding: &WalletFunding{userId: 7},
	}

	err := session.Reserve(101)

	require.Error(t, err)
	var apiErr *types.NewAPIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
	require.Zero(t, session.preConsumedQuota)
	require.Zero(t, session.tokenConsumed)

	var user model.User
	require.NoError(t, model.DB.Select("quota").First(&user, 7).Error)
	require.Equal(t, 100, user.Quota)
	var token model.Token
	require.NoError(t, model.DB.Select("remain_quota", "used_quota").First(&token, 8).Error)
	require.Equal(t, 1000, token.RemainQuota)
	require.Zero(t, token.UsedQuota)
}
