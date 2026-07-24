package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	TaskBillingStatusReserved   = "reserved"
	TaskBillingStatusProcessing = "processing"
	TaskBillingStatusSettled    = "settled"
	TaskBillingStatusRefunded   = "refunded"
)

var (
	ErrTaskBillingConflict = errors.New("task billing state conflict")
	ErrInvalidTaskBilling  = errors.New("invalid task billing transition")
)

// TaskBillingResult describes the durable balance mutation committed with a
// terminal task transition. Applied is false for an already-finalized task, so
// callers can suppress duplicate logs and usage counters.
type TaskBillingResult struct {
	Applied          bool
	BillingStatus    string
	PreviousQuota    int
	ActualQuota      int
	QuotaDelta       int
	RefundedQuota    int
	UserID           int
	ChannelID        int
	TokenID          int
	SubscriptionID   int
	UsedSubscription bool
}

// FinalizeTaskBilling atomically moves an active task to SUCCESS/FAILURE and
// applies the matching wallet/subscription and token settlement. The transient
// processing marker is written with a conditional UPDATE inside the same
// transaction, making the primary task row the idempotency record on all
// supported databases (including SQLite, where FOR UPDATE is unavailable).
//
// Existing active rows with an empty billing_status are treated as reserved.
// Existing terminal rows with an empty billing_status are treated as legacy
// rows and are never replayed, because their historical refund state cannot be
// determined safely.
func FinalizeTaskBilling(finalTask *Task, actualQuota int) (TaskBillingResult, error) {
	result := TaskBillingResult{}
	if finalTask == nil || finalTask.ID <= 0 || actualQuota < 0 || actualQuota > common.MaxQuota {
		return result, ErrInvalidTaskBilling
	}
	if finalTask.Status != TaskStatusSuccess && finalTask.Status != TaskStatusFailure {
		return result, ErrInvalidTaskBilling
	}
	if finalTask.Status == TaskStatusFailure && actualQuota != 0 {
		return result, ErrInvalidTaskBilling
	}

	var tokenKey string
	var committedStartTime int64
	var committedFinishTime int64
	var committedFailReason string
	var committedProgress string
	var committedPrivateData TaskPrivateData
	var committedData []byte
	err := DB.Transaction(func(tx *gorm.DB) error {
		var persisted Task
		if err := lockForUpdate(tx).Where("id = ?", finalTask.ID).First(&persisted).Error; err != nil {
			return err
		}
		if persisted.Quota < 0 || persisted.Quota > common.MaxQuota {
			return ErrInvalidTaskBilling
		}

		result = TaskBillingResult{
			BillingStatus:    persisted.BillingStatus,
			PreviousQuota:    persisted.Quota,
			ActualQuota:      persisted.Quota,
			UserID:           persisted.UserId,
			ChannelID:        persisted.ChannelId,
			TokenID:          persisted.PrivateData.TokenId,
			SubscriptionID:   persisted.PrivateData.SubscriptionId,
			UsedSubscription: persisted.PrivateData.BillingSource == "subscription" && persisted.PrivateData.SubscriptionId > 0,
		}

		switch persisted.BillingStatus {
		case TaskBillingStatusSettled, TaskBillingStatusRefunded:
			return nil
		case "":
			if persisted.Status == TaskStatusSuccess || persisted.Status == TaskStatusFailure {
				// Do not replay a terminal task created before the billing marker existed.
				return nil
			}
		case TaskBillingStatusReserved:
		default:
			return fmt.Errorf("%w: task %d has status %q", ErrTaskBillingConflict, persisted.ID, persisted.BillingStatus)
		}

		claim := tx.Model(&Task{}).
			Where("id = ?", persisted.ID).
			Where("status NOT IN ?", []TaskStatus{TaskStatusSuccess, TaskStatusFailure}).
			Where("billing_status = ? OR billing_status = ? OR billing_status IS NULL", "", TaskBillingStatusReserved).
			Updates(map[string]interface{}{
				"billing_status":     TaskBillingStatusProcessing,
				"billing_updated_at": common.GetTimestamp(),
			})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected != 1 {
			return ErrTaskBillingConflict
		}

		quotaDelta := actualQuota - persisted.Quota
		if err := adjustTaskFundingTx(tx, &persisted, quotaDelta); err != nil {
			return err
		}
		if persisted.PrivateData.TokenId > 0 && quotaDelta != 0 {
			var token Token
			tokenErr := tx.Select("id", "key").Where("id = ?", persisted.PrivateData.TokenId).First(&token).Error
			if tokenErr != nil && !errors.Is(tokenErr, gorm.ErrRecordNotFound) {
				return tokenErr
			}
			if tokenErr == nil {
				tokenKey = token.Key
				if quotaDelta > 0 {
					if err := DecreaseTokenQuotaTx(tx, token.Id, quotaDelta); err != nil {
						return err
					}
				} else {
					if err := IncreaseTokenQuotaTx(tx, token.Id, -quotaDelta); err != nil {
						return err
					}
				}
			} else if quotaDelta > 0 {
				return gorm.ErrRecordNotFound
			}
		}

		billingStatus := TaskBillingStatusSettled
		refundedQuota := 0
		if finalTask.Status == TaskStatusFailure {
			billingStatus = TaskBillingStatusRefunded
			refundedQuota = persisted.Quota
		} else if quotaDelta < 0 {
			refundedQuota = -quotaDelta
		}
		now := common.GetTimestamp()
		privateData := persisted.PrivateData
		if finalTask.PrivateData.ResultURL != "" {
			privateData.ResultURL = finalTask.PrivateData.ResultURL
		}
		startTime := finalTask.StartTime
		if startTime == 0 {
			startTime = persisted.StartTime
		}
		finishTime := finalTask.FinishTime
		if finishTime == 0 {
			finishTime = persisted.FinishTime
		}
		failReason := finalTask.FailReason
		if failReason == "" {
			failReason = persisted.FailReason
		}
		progress := finalTask.Progress
		if progress == "" {
			progress = persisted.Progress
		}
		data := finalTask.Data
		if len(data) == 0 {
			data = persisted.Data
		}
		updates := map[string]interface{}{
			"status":             finalTask.Status,
			"progress":           progress,
			"start_time":         startTime,
			"finish_time":        finishTime,
			"fail_reason":        failReason,
			"private_data":       privateData,
			"data":               data,
			"quota":              actualQuota,
			"billing_status":     billingStatus,
			"refunded_quota":     refundedQuota,
			"billing_updated_at": now,
			"updated_at":         now,
		}
		finalize := tx.Model(&Task{}).
			Where("id = ? AND billing_status = ?", persisted.ID, TaskBillingStatusProcessing).
			Updates(updates)
		if finalize.Error != nil {
			return finalize.Error
		}
		if finalize.RowsAffected != 1 {
			return ErrTaskBillingConflict
		}

		result.Applied = true
		result.BillingStatus = billingStatus
		result.ActualQuota = actualQuota
		result.QuotaDelta = quotaDelta
		result.RefundedQuota = refundedQuota
		committedStartTime = startTime
		committedFinishTime = finishTime
		committedFailReason = failReason
		committedProgress = progress
		committedPrivateData = privateData
		committedData = append(committedData[:0], data...)
		return nil
	})
	if err != nil {
		return TaskBillingResult{}, err
	}
	if !result.Applied {
		return result, nil
	}

	if !result.UsedSubscription {
		invalidateBillingQuotaCaches(result.UserID, tokenKey)
	} else if tokenKey != "" {
		invalidateBillingQuotaCaches(0, tokenKey)
	}
	finalTask.Quota = result.ActualQuota
	finalTask.BillingStatus = result.BillingStatus
	finalTask.RefundedQuota = result.RefundedQuota
	finalTask.BillingUpdatedAt = common.GetTimestamp()
	finalTask.StartTime = committedStartTime
	finalTask.FinishTime = committedFinishTime
	finalTask.FailReason = committedFailReason
	finalTask.Progress = committedProgress
	finalTask.PrivateData = committedPrivateData
	finalTask.Data = committedData
	return result, nil
}

func adjustTaskFundingTx(tx *gorm.DB, task *Task, delta int) error {
	if delta == 0 {
		return nil
	}
	if task.PrivateData.BillingSource == "subscription" && task.PrivateData.SubscriptionId > 0 {
		return AdjustUserSubscriptionDeltaTx(tx, task.PrivateData.SubscriptionId, int64(delta))
	}
	if delta > 0 {
		return DecreaseUserQuotaTx(tx, task.UserId, delta)
	}
	return IncreaseUserQuotaTx(tx, task.UserId, -delta)
}
