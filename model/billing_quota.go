package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// DecreaseUserQuotaTx conditionally consumes wallet quota inside tx. It is
// safe under concurrent requests: a balance can never be committed below zero.
// Cache invalidation is deliberately left to the owner of the transaction and
// must happen only after commit.
func DecreaseUserQuotaTx(tx *gorm.DB, userID int, quota int) error {
	if tx == nil {
		return errors.New("database transaction is nil")
	}
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	if quota == 0 {
		return nil
	}

	result := tx.Model(&User{}).
		Where("id = ? AND quota >= ?", userID, quota).
		Update("quota", gorm.Expr("quota - ?", quota))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInsufficientUserQuota
	}
	return nil
}

// IncreaseUserQuotaTx refunds or grants wallet quota inside tx. Cache
// invalidation is the responsibility of the transaction owner after commit.
func IncreaseUserQuotaTx(tx *gorm.DB, userID int, quota int) error {
	if tx == nil {
		return errors.New("database transaction is nil")
	}
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	if quota == 0 {
		return nil
	}

	result := tx.Model(&User{}).
		Where("id = ?", userID).
		Update("quota", gorm.Expr("quota + ?", quota))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// DecreaseTokenQuotaTx conditionally consumes token quota inside tx. Unlimited
// tokens retain their configured remaining balance while used_quota continues
// to record consumption.
func DecreaseTokenQuotaTx(tx *gorm.DB, tokenID int, quota int) error {
	if tx == nil {
		return errors.New("database transaction is nil")
	}
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	if quota == 0 {
		return nil
	}

	result := tx.Model(&Token{}).
		Where("id = ? AND (unlimited_quota = ? OR remain_quota >= ?)", tokenID, true, quota).
		Updates(map[string]interface{}{
			"remain_quota": gorm.Expr(
				"CASE WHEN unlimited_quota = ? THEN remain_quota ELSE remain_quota - ? END",
				true,
				quota,
			),
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"accessed_time": common.GetTimestamp(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInsufficientTokenQuota
	}
	return nil
}

// IncreaseTokenQuotaTx refunds token quota inside tx. A deleted token does not
// block a wallet refund. used_quota preserves the existing delta-accounting
// semantics because historical refund paths may intentionally make it negative.
func IncreaseTokenQuotaTx(tx *gorm.DB, tokenID int, quota int) error {
	if tx == nil {
		return errors.New("database transaction is nil")
	}
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	if quota == 0 {
		return nil
	}

	return tx.Model(&Token{}).
		Where("id = ?", tokenID).
		Updates(map[string]interface{}{
			"remain_quota": gorm.Expr(
				"CASE WHEN unlimited_quota = ? THEN remain_quota ELSE remain_quota + ? END",
				true,
				quota,
			),
			"used_quota":    gorm.Expr("used_quota - ?", quota),
			"accessed_time": common.GetTimestamp(),
		}).Error
}

// AdjustWalletAndTokenQuota atomically adjusts a wallet and its authorizing
// token. A positive delta consumes quota; a negative delta refunds it. The
// database commits both sides or neither side, then the caches are invalidated.
func AdjustWalletAndTokenQuota(userID int, tokenID int, tokenKey string, delta int, skipToken bool) error {
	if delta == 0 {
		return nil
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		if delta > 0 {
			if err := DecreaseUserQuotaTx(tx, userID, delta); err != nil {
				return err
			}
			if !skipToken {
				if err := DecreaseTokenQuotaTx(tx, tokenID, delta); err != nil {
					return err
				}
			}
			return nil
		}

		refund := -delta
		if err := IncreaseUserQuotaTx(tx, userID, refund); err != nil {
			return err
		}
		if !skipToken {
			return IncreaseTokenQuotaTx(tx, tokenID, refund)
		}
		return nil
	})
	if err != nil {
		return err
	}

	invalidateBillingQuotaCaches(userID, tokenKey)
	return nil
}

func invalidateBillingQuotaCaches(userID int, tokenKey string) {
	if !common.RedisEnabled {
		return
	}
	if userID > 0 {
		if err := invalidateUserCache(userID); err != nil {
			common.SysLog("failed to invalidate user quota cache after database commit: " + err.Error())
		}
	}
	if tokenKey == "" {
		return
	}
	if err := cacheDeleteToken(tokenKey); err != nil {
		common.SysLog("failed to invalidate token quota cache after database commit: " + err.Error())
	}
}
