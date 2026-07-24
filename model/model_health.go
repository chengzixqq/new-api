package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/model_health_setting"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ModelHealthModeLocal    = "local"
	ModelHealthModeUpstream = "upstream"

	ModelHealthProtocolOpenAIChat        = "openai_chat"
	ModelHealthProtocolOpenAIResponses   = "openai_responses"
	ModelHealthProtocolAnthropicMessages = "anthropic_messages"
	ModelHealthProtocolGeminiContent     = "gemini_generate_content"

	ModelHealthProbeSuccess         = "success"
	ModelHealthProbeFailure         = "failure"
	ModelHealthProbeTimeout         = "timeout"
	ModelHealthProbeAuthFailure     = "auth_failure"
	ModelHealthProbeInvalidResponse = "invalid_response"

	ModelHealthSamplingFixed            = "fixed"
	ModelHealthSamplingConfirmOnFailure = "confirm_on_failure"

	MaxModelHealthTargets         = 100
	MaxModelsPerModelHealthTarget = 20
	MaxObservedGroupsPerTarget    = 20
	MaxObservedChannelsPerTarget  = 20
)

var (
	ErrModelHealthProbeTokenInUse   = errors.New("token is used by a model health target")
	ErrModelHealthPoolAliasConflict = errors.New("public pool alias is already used in this public group")
)

type ModelHealthTargetModel struct {
	Name        string `json:"name"`
	PublicAlias string `json:"public_alias"`
	Required    bool   `json:"required"`
}

// ModelHealthTarget contains only encrypted upstream credentials. The
// ciphertext fields and internal model JSON are never serialized directly;
// admin handlers should build an explicit response DTO instead.
type ModelHealthTarget struct {
	ID                     int64  `json:"id" gorm:"primaryKey"`
	Name                   string `json:"name" gorm:"type:varchar(128);index"`
	Mode                   string `json:"mode" gorm:"type:varchar(16);index"`
	TokenID                *int   `json:"token_id,omitempty" gorm:"index"`
	Protocol               string `json:"protocol" gorm:"type:varchar(48)"`
	EndpointEncrypted      string `json:"-" gorm:"column:endpoint_encrypted;type:text"`
	APIKeyEncrypted        string `json:"-" gorm:"column:api_key_encrypted;type:text"`
	CredentialFingerprint  string `json:"-" gorm:"type:varchar(32)"`
	ModelsJSON             string `json:"-" gorm:"column:models;type:text"`
	ObservedGroup          string `json:"observed_group,omitempty" gorm:"type:varchar(64);index"`
	ObservedGroupsJSON     string `json:"-" gorm:"column:observed_groups;type:text"`
	ObservedChannelIDsJSON string `json:"-" gorm:"column:observed_channel_ids;type:text"`
	PublicGroupAlias       string `json:"public_group_alias,omitempty" gorm:"type:varchar(128);index"`
	PublicPoolAlias        string `json:"public_pool_alias,omitempty" gorm:"type:varchar(128);index"`
	PublishPoolDetail      bool   `json:"publish_pool_detail" gorm:"index"`
	Public                 bool   `json:"public" gorm:"index"`
	Enabled                bool   `json:"enabled" gorm:"index"`
	IntervalSeconds        int    `json:"interval_seconds"`
	TimeoutSeconds         int    `json:"timeout_seconds"`
	SamplingMode           string `json:"sampling_mode" gorm:"type:varchar(32)"`
	SamplesPerRun          int    `json:"samples_per_run"`
	MinimumSuccesses       int    `json:"minimum_successes"`
	SampleSpacingSeconds   int    `json:"sample_spacing_seconds"`
	LatencySLOMs           int64  `json:"latency_slo_ms"`
	LastCheckedAt          int64  `json:"last_checked_at" gorm:"bigint;index"`
	CreatedAt              int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt              int64  `json:"updated_at" gorm:"bigint"`
}

func (ModelHealthTarget) TableName() string { return "model_health_targets" }

func (target *ModelHealthTarget) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if target.CreatedAt == 0 {
		target.CreatedAt = now
	}
	if target.UpdatedAt == 0 {
		target.UpdatedAt = now
	}
	return target.Normalize()
}

func (target *ModelHealthTarget) Models() ([]ModelHealthTargetModel, error) {
	var models []ModelHealthTargetModel
	if err := common.Unmarshal([]byte(target.ModelsJSON), &models); err != nil {
		return nil, fmt.Errorf("invalid model configuration: %w", err)
	}
	return models, nil
}

func (target *ModelHealthTarget) SetModels(models []ModelHealthTargetModel) error {
	encoded, err := common.Marshal(models)
	if err != nil {
		return err
	}
	target.ModelsJSON = string(encoded)
	return nil
}

// ObservedGroups returns the passive-traffic groups selected for this target.
// Rows written before observed_groups was introduced transparently fall back
// to observed_group so rolling upgrades and application rollbacks stay usable.
func (target *ModelHealthTarget) ObservedGroups() ([]string, error) {
	if strings.TrimSpace(target.ObservedGroupsJSON) == "" {
		if group := strings.TrimSpace(target.ObservedGroup); group != "" {
			return []string{group}, nil
		}
		return []string{}, nil
	}
	var groups []string
	if err := common.Unmarshal([]byte(target.ObservedGroupsJSON), &groups); err != nil {
		return nil, fmt.Errorf("invalid observed group configuration: %w", err)
	}
	return groups, nil
}

func (target *ModelHealthTarget) SetObservedGroups(groups []string) error {
	encoded, err := common.Marshal(groups)
	if err != nil {
		return err
	}
	target.ObservedGroupsJSON = string(encoded)
	target.ObservedGroup = ""
	if len(groups) > 0 {
		target.ObservedGroup = groups[0]
	}
	return nil
}

// ObservedChannelIDs returns the channel bindings used only for pool-level
// passive traffic evidence. IDs remain stable across channel renames and are
// intentionally stored separately from the public pool alias.
func (target *ModelHealthTarget) ObservedChannelIDs() ([]int, error) {
	if strings.TrimSpace(target.ObservedChannelIDsJSON) == "" {
		return []int{}, nil
	}
	var channelIDs []int
	if err := common.Unmarshal([]byte(target.ObservedChannelIDsJSON), &channelIDs); err != nil {
		return nil, fmt.Errorf("invalid observed channel configuration: %w", err)
	}
	return channelIDs, nil
}

func (target *ModelHealthTarget) SetObservedChannelIDs(channelIDs []int) error {
	encoded, err := common.Marshal(channelIDs)
	if err != nil {
		return err
	}
	target.ObservedChannelIDsJSON = string(encoded)
	return nil
}

func (target *ModelHealthTarget) Normalize() error {
	target.Name = strings.TrimSpace(target.Name)
	target.Mode = strings.TrimSpace(target.Mode)
	target.Protocol = strings.TrimSpace(target.Protocol)
	target.PublicGroupAlias = strings.TrimSpace(target.PublicGroupAlias)
	target.PublicPoolAlias = strings.TrimSpace(target.PublicPoolAlias)
	groups, err := target.ObservedGroups()
	if err != nil {
		return err
	}
	normalizedGroups := make([]string, 0, len(groups))
	seenGroups := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		if len(group) > 64 {
			return errors.New("group name is too long")
		}
		if _, exists := seenGroups[group]; exists {
			continue
		}
		seenGroups[group] = struct{}{}
		normalizedGroups = append(normalizedGroups, group)
	}
	if len(normalizedGroups) > MaxObservedGroupsPerTarget {
		return fmt.Errorf("observed_groups must contain at most %d entries", MaxObservedGroupsPerTarget)
	}
	if err := target.SetObservedGroups(normalizedGroups); err != nil {
		return err
	}
	channelIDs, err := target.ObservedChannelIDs()
	if err != nil {
		return err
	}
	normalizedChannelIDs := make([]int, 0, len(channelIDs))
	seenChannelIDs := make(map[int]struct{}, len(channelIDs))
	for _, channelID := range channelIDs {
		if channelID <= 0 {
			return errors.New("observed_channel_ids must contain positive channel IDs")
		}
		if _, exists := seenChannelIDs[channelID]; exists {
			continue
		}
		seenChannelIDs[channelID] = struct{}{}
		normalizedChannelIDs = append(normalizedChannelIDs, channelID)
	}
	if len(normalizedChannelIDs) > MaxObservedChannelsPerTarget {
		return fmt.Errorf("observed_channel_ids must contain at most %d entries", MaxObservedChannelsPerTarget)
	}
	if err := target.SetObservedChannelIDs(normalizedChannelIDs); err != nil {
		return err
	}

	if target.Name == "" || len(target.Name) > 128 {
		return errors.New("target name must contain 1 to 128 characters")
	}
	if target.Mode != ModelHealthModeLocal && target.Mode != ModelHealthModeUpstream {
		return errors.New("unsupported model health target mode")
	}
	if !ValidModelHealthProtocol(target.Protocol) {
		return errors.New("unsupported model health protocol")
	}
	if target.IntervalSeconds < 60 || target.IntervalSeconds > 3600 {
		return errors.New("interval_seconds must be between 60 and 3600")
	}
	if target.TimeoutSeconds < 1 || target.TimeoutSeconds > 300 {
		return errors.New("timeout_seconds must be between 1 and 300")
	}
	legacySampling := strings.TrimSpace(target.SamplingMode) == "" && target.SamplesPerRun == 0 && target.MinimumSuccesses == 0
	target.SamplingMode = strings.TrimSpace(target.SamplingMode)
	if legacySampling {
		target.SamplingMode = ModelHealthSamplingFixed
		target.SamplesPerRun = 1
		target.MinimumSuccesses = 1
	}
	if target.SampleSpacingSeconds == 0 {
		target.SampleSpacingSeconds = model_health_setting.DefaultSampleSpacingSeconds
	}
	if target.SamplingMode != ModelHealthSamplingFixed && target.SamplingMode != ModelHealthSamplingConfirmOnFailure {
		return errors.New("sampling_mode must be fixed or confirm_on_failure")
	}
	if target.SamplesPerRun < 1 || target.SamplesPerRun > 5 {
		return errors.New("samples_per_run must be between 1 and 5")
	}
	if target.SamplesPerRun == 1 {
		target.MinimumSuccesses = 1
	}
	if target.MinimumSuccesses < 1 || target.MinimumSuccesses > target.SamplesPerRun {
		return errors.New("minimum_successes must be between 1 and samples_per_run")
	}
	if target.SampleSpacingSeconds < 1 || target.SampleSpacingSeconds > 30 {
		return errors.New("sample_spacing_seconds must be between 1 and 30")
	}
	probeBudget := target.SamplesPerRun*target.TimeoutSeconds + (target.SamplesPerRun-1)*target.SampleSpacingSeconds
	if probeBudget > target.IntervalSeconds {
		return errors.New("probe sampling time budget must not exceed interval_seconds")
	}
	if target.LatencySLOMs < 0 {
		return errors.New("latency_slo_ms must not be negative")
	}
	if len(target.PublicGroupAlias) > 128 {
		return errors.New("group name is too long")
	}
	if len(target.PublicPoolAlias) > 128 {
		return errors.New("pool name is too long")
	}
	if target.PublishPoolDetail {
		if target.PublicGroupAlias == "" {
			return errors.New("public_group_alias is required when pool details are published")
		}
		if target.PublicPoolAlias == "" {
			return errors.New("public_pool_alias is required when pool details are published")
		}
		if len(normalizedChannelIDs) == 0 {
			return errors.New("observed_channel_ids is required when pool details are published")
		}
	}

	models, err := target.Models()
	if err != nil {
		return err
	}
	if len(models) == 0 || len(models) > MaxModelsPerModelHealthTarget {
		return fmt.Errorf("models must contain 1 to %d entries", MaxModelsPerModelHealthTarget)
	}
	seenNames := make(map[string]struct{}, len(models))
	seenAliases := make(map[string]struct{}, len(models))
	for i := range models {
		models[i].Name = strings.TrimSpace(models[i].Name)
		models[i].PublicAlias = strings.TrimSpace(models[i].PublicAlias)
		if models[i].Name == "" || len(models[i].Name) > 128 {
			return fmt.Errorf("model %d name must contain 1 to 128 characters", i+1)
		}
		if len(models[i].PublicAlias) > 128 {
			return fmt.Errorf("model %d public alias is too long", i+1)
		}
		if _, exists := seenNames[models[i].Name]; exists {
			return fmt.Errorf("duplicate model name: %s", models[i].Name)
		}
		seenNames[models[i].Name] = struct{}{}
		if models[i].PublicAlias != "" {
			if _, exists := seenAliases[models[i].PublicAlias]; exists {
				return fmt.Errorf("duplicate public model alias: %s", models[i].PublicAlias)
			}
			seenAliases[models[i].PublicAlias] = struct{}{}
		}
	}
	if err := target.SetModels(models); err != nil {
		return err
	}

	switch target.Mode {
	case ModelHealthModeLocal:
		if target.TokenID == nil || *target.TokenID <= 0 {
			return errors.New("local target requires token_id")
		}
		target.EndpointEncrypted = ""
		target.APIKeyEncrypted = ""
		target.CredentialFingerprint = ""
	case ModelHealthModeUpstream:
		if target.EndpointEncrypted == "" || target.APIKeyEncrypted == "" {
			return errors.New("upstream target requires encrypted endpoint and API key")
		}
		target.TokenID = nil
	}
	return nil
}

func ValidModelHealthProtocol(protocol string) bool {
	switch protocol {
	case ModelHealthProtocolOpenAIChat,
		ModelHealthProtocolOpenAIResponses,
		ModelHealthProtocolAnthropicMessages,
		ModelHealthProtocolGeminiContent:
		return true
	default:
		return false
	}
}

// ModelHealthHistory stores only sanitized outcome data. Raw response bodies,
// upstream errors, endpoints, and credentials must never be persisted here.
type ModelHealthHistory struct {
	ID               int64  `json:"id" gorm:"primaryKey"`
	TargetID         int64  `json:"target_id" gorm:"index:idx_model_health_history_lookup,priority:1;index:idx_model_health_history_run,priority:1;index"`
	ModelName        string `json:"-" gorm:"type:varchar(128);index:idx_model_health_history_lookup,priority:2;index:idx_model_health_history_run,priority:2"`
	ProbeRunID       string `json:"-" gorm:"type:varchar(36);index:idx_model_health_history_run,priority:3"`
	RunStartedAt     int64  `json:"-" gorm:"bigint"`
	AttemptIndex     int    `json:"-"`
	SamplingMode     string `json:"-" gorm:"type:varchar(32)"`
	PlannedAttempts  int    `json:"-"`
	MinimumSuccesses int    `json:"-"`
	Status           string `json:"status" gorm:"type:varchar(32);index"`
	LatencyMs        int64  `json:"latency_ms"`
	HTTPStatus       int    `json:"http_status"`
	ErrorClass       string `json:"error_class,omitempty" gorm:"type:varchar(48)"`
	CheckedAt        int64  `json:"checked_at" gorm:"bigint;index:idx_model_health_history_lookup,priority:3,sort:desc;index"`
}

func (ModelHealthHistory) TableName() string { return "model_health_histories" }

// ModelHealthProbeToken marks an existing token as eligible for local health
// probes. It deliberately stores only the token identifier and marking actor;
// the token secret remains exclusively in the tokens table.
type ModelHealthProbeToken struct {
	TokenID   int   `json:"token_id" gorm:"primaryKey;autoIncrement:false"`
	MarkedBy  int   `json:"marked_by" gorm:"index"`
	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

func (ModelHealthProbeToken) TableName() string { return "model_health_probe_tokens" }

type ModelHealthTokenReferenceCount struct {
	TokenID int   `json:"token_id"`
	Count   int64 `json:"count"`
}

// ListModelHealthProbeTokenCandidates returns only non-secret token metadata
// for the requested owner. Callers must still apply availability rules because
// exhausted and expired tokens remain visible in the management list.
func ListModelHealthProbeTokenCandidates(userID int) ([]Token, error) {
	if userID <= 0 {
		return nil, errors.New("invalid probe token owner")
	}
	var tokens []Token
	err := DB.Select("id", "user_id", "status", "name", "expired_time", "remain_quota", "unlimited_quota", "model_limits_enabled", "model_limits", commonGroupCol).
		Where("user_id = ?", userID).
		Order("id DESC").
		Find(&tokens).Error
	return tokens, err
}

func ListModelHealthProbeTokenMarks(tokenIDs []int) ([]ModelHealthProbeToken, error) {
	if len(tokenIDs) == 0 {
		return []ModelHealthProbeToken{}, nil
	}
	var marks []ModelHealthProbeToken
	err := DB.Where("token_id IN ?", tokenIDs).Find(&marks).Error
	return marks, err
}

func GetModelHealthProbeTokenMark(tokenID int) (*ModelHealthProbeToken, error) {
	if tokenID <= 0 {
		return nil, errors.New("invalid probe token id")
	}
	var mark ModelHealthProbeToken
	if err := DB.First(&mark, "token_id = ?", tokenID).Error; err != nil {
		return nil, err
	}
	return &mark, nil
}

func ModelHealthTokenReferenceCounts(tokenIDs []int) (map[int]int64, error) {
	counts := make(map[int]int64, len(tokenIDs))
	if len(tokenIDs) == 0 {
		return counts, nil
	}
	var rows []ModelHealthTokenReferenceCount
	err := DB.Model(&ModelHealthTarget{}).
		Select("token_id, COUNT(*) AS count").
		Where("mode = ? AND token_id IN ?", ModelHealthModeLocal, tokenIDs).
		Group("token_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		counts[row.TokenID] = row.Count
	}
	return counts, nil
}

func SetModelHealthProbeTokenMarked(tokenID, userID int, marked bool) error {
	if tokenID <= 0 || userID <= 0 {
		return errors.New("invalid probe token selection")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var token Token
		if err := tx.Select("id", "user_id").Where("id = ? AND user_id = ?", tokenID, userID).First(&token).Error; err != nil {
			return err
		}
		if marked {
			now := common.GetTimestamp()
			candidate := ModelHealthProbeToken{
				TokenID: tokenID, MarkedBy: userID, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
				return err
			}
			var mark ModelHealthProbeToken
			if err := tx.First(&mark, "token_id = ?", tokenID).Error; err != nil {
				return err
			}
			if mark.MarkedBy != userID {
				return errors.New("probe token is marked by another owner")
			}
			return nil
		}

		var references int64
		if err := tx.Model(&ModelHealthTarget{}).Where("mode = ? AND token_id = ?", ModelHealthModeLocal, tokenID).Count(&references).Error; err != nil {
			return err
		}
		if references > 0 {
			return fmt.Errorf("%w: token %d has %d target references", ErrModelHealthProbeTokenInUse, tokenID, references)
		}
		return tx.Where("token_id = ? AND marked_by = ?", tokenID, userID).Delete(&ModelHealthProbeToken{}).Error
	})
}

func IsModelHealthProbeTokenMarkedForOwner(tokenID, userID int) (bool, error) {
	if tokenID <= 0 || userID <= 0 {
		return false, nil
	}
	var count int64
	err := DB.Model(&ModelHealthProbeToken{}).
		Where("token_id = ? AND marked_by = ?", tokenID, userID).
		Count(&count).Error
	return count > 0, err
}

// ListEnabledModelHealthAbilities exposes the model x group option matrix
// without channel identifiers, names, URLs, or credentials.
func ListEnabledModelHealthAbilities() ([]Ability, error) {
	var abilities []Ability
	err := DB.Model(&Ability{}).
		Select(commonGroupCol+", model").
		Where("enabled = ?", true).
		Group(commonGroupCol + ", model").
		Order(commonGroupCol + " ASC, model ASC").
		Find(&abilities).Error
	return abilities, err
}

// ListModelHealthChannelOptions returns the minimal channel metadata needed by
// the model-health admin selector. Secret-bearing fields are never selected.
func ListModelHealthChannelOptions() ([]Channel, error) {
	var channels []Channel
	err := DB.Model(&Channel{}).
		Select("id", "name", "status", "models", commonGroupCol).
		Order("id ASC").
		Find(&channels).Error
	return channels, err
}

func CountEnabledModelHealthChannels(channelIDs []int) (int64, error) {
	if len(channelIDs) == 0 {
		return 0, nil
	}
	var count int64
	err := DB.Model(&Channel{}).Where("id IN ? AND status = ?", channelIDs, common.ChannelStatusEnabled).Count(&count).Error
	return count, err
}

func validateModelHealthPoolAlias(tx *gorm.DB, target *ModelHealthTarget) error {
	if target == nil || !target.PublishPoolDetail {
		return nil
	}
	query := tx.Model(&ModelHealthTarget{}).
		Where("publish_pool_detail = ? AND public_group_alias = ? AND public_pool_alias = ?", true, target.PublicGroupAlias, target.PublicPoolAlias)
	if target.ID > 0 {
		query = query.Where("id <> ?", target.ID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: %s / %s", ErrModelHealthPoolAliasConflict, target.PublicGroupAlias, target.PublicPoolAlias)
	}
	return nil
}

func ValidateModelHealthPoolAlias(target *ModelHealthTarget) error {
	return validateModelHealthPoolAlias(DB, target)
}

func CreateModelHealthTarget(target *ModelHealthTarget) error {
	if target == nil {
		return errors.New("model health target is nil")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&ModelHealthTarget{}).Count(&count).Error; err != nil {
			return err
		}
		if count >= MaxModelHealthTargets {
			return fmt.Errorf("model health target limit reached (%d)", MaxModelHealthTargets)
		}
		if err := validateModelHealthPoolAlias(tx, target); err != nil {
			return err
		}
		return tx.Create(target).Error
	})
}

func SaveModelHealthTarget(target *ModelHealthTarget) error {
	if target == nil || target.ID <= 0 {
		return errors.New("invalid model health target")
	}
	if err := target.Normalize(); err != nil {
		return err
	}
	target.UpdatedAt = common.GetTimestamp()
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := validateModelHealthPoolAlias(tx, target); err != nil {
			return err
		}
		return tx.Save(target).Error
	})
}

func GetModelHealthTarget(id int64) (*ModelHealthTarget, error) {
	if id <= 0 {
		return nil, errors.New("invalid model health target id")
	}
	var target ModelHealthTarget
	if err := DB.First(&target, id).Error; err != nil {
		return nil, err
	}
	return &target, nil
}

func ListModelHealthTargets() ([]ModelHealthTarget, error) {
	var targets []ModelHealthTarget
	err := DB.Order("id ASC").Find(&targets).Error
	return targets, err
}

func ListPublicModelHealthTargets() ([]ModelHealthTarget, error) {
	var targets []ModelHealthTarget
	err := DB.Where("enabled = ? AND public = ?", true, true).Order("id ASC").Find(&targets).Error
	return targets, err
}

func ListDueModelHealthTargets(now int64, limit int) ([]ModelHealthTarget, error) {
	if limit <= 0 || limit > MaxModelHealthTargets {
		limit = MaxModelHealthTargets
	}
	var candidates []ModelHealthTarget
	if err := DB.Where("enabled = ?", true).
		Order("last_checked_at ASC, id ASC").
		Limit(limit).
		Find(&candidates).Error; err != nil {
		return nil, err
	}
	due := make([]ModelHealthTarget, 0, len(candidates))
	for _, target := range candidates {
		if target.LastCheckedAt == 0 || target.LastCheckedAt+int64(target.IntervalSeconds) <= now {
			due = append(due, target)
		}
	}
	return due, nil
}

func MarkModelHealthTargetChecked(id int64, checkedAt int64) error {
	return DB.Model(&ModelHealthTarget{}).Where("id = ?", id).Updates(map[string]any{
		"last_checked_at": checkedAt,
		"updated_at":      common.GetTimestamp(),
	}).Error
}

func DisableModelHealthTarget(id int64) error {
	return DB.Model(&ModelHealthTarget{}).Where("id = ?", id).Updates(map[string]any{
		"enabled":    false,
		"updated_at": common.GetTimestamp(),
	}).Error
}

func DeleteModelHealthTarget(id int64) error {
	if id <= 0 {
		return errors.New("invalid model health target id")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("target_id = ?", id).Delete(&ModelHealthHistory{}).Error; err != nil {
			return err
		}
		result := tx.Delete(&ModelHealthTarget{}, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func InsertModelHealthHistories(histories []ModelHealthHistory) error {
	if len(histories) == 0 {
		return nil
	}
	return DB.CreateInBatches(histories, 100).Error
}

func RecordModelHealthTargetRun(targetID, checkedAt int64, histories []ModelHealthHistory) error {
	if targetID <= 0 || checkedAt <= 0 {
		return errors.New("invalid model health run")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if len(histories) > 0 {
			for i := range histories {
				histories[i].TargetID = targetID
				if histories[i].CheckedAt == 0 {
					histories[i].CheckedAt = checkedAt
				}
			}
			if err := tx.CreateInBatches(histories, 100).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&ModelHealthTarget{}).Where("id = ?", targetID).Updates(map[string]any{
			"last_checked_at": checkedAt,
			"updated_at":      common.GetTimestamp(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func GetModelHealthHistories(targetIDs []int64, modelNames []string, startAt, endAt int64) ([]ModelHealthHistory, error) {
	if len(targetIDs) == 0 {
		return []ModelHealthHistory{}, nil
	}
	query := DB.Where("target_id IN ? AND checked_at >= ? AND checked_at <= ?", targetIDs, startAt, endAt)
	if modelNames != nil {
		if len(modelNames) == 0 {
			return []ModelHealthHistory{}, nil
		}
		query = query.Where("model_name IN ?", modelNames)
	}
	var histories []ModelHealthHistory
	err := query.Order("checked_at ASC, id ASC").Find(&histories).Error
	return histories, err
}

func DeleteModelHealthHistoriesBefore(cutoff int64) error {
	if cutoff <= 0 {
		return nil
	}
	return DB.Where("checked_at < ?", cutoff).Delete(&ModelHealthHistory{}).Error
}

func ModelHealthTargetIDs(targets []ModelHealthTarget) []int64 {
	ids := make([]int64, 0, len(targets))
	for _, target := range targets {
		if target.ID > 0 {
			ids = append(ids, target.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// MigrateLegacyModelHealthConfiguration is an idempotent post-option-load
// migration. It marks tokens referenced by existing local targets, preserves
// legacy one-attempt sampling, and copies global aliases only where target-local aliases are empty.
// The legacy options remain untouched for rollback and historical inspection.
func MigrateLegacyModelHealthConfiguration() error {
	const aliasMigrationMarker = "ModelHealthTargetAliasMigrationV1"
	targets, err := ListModelHealthTargets()
	if err != nil {
		return err
	}
	var existingMarker Option
	markerErr := DB.Where(&Option{Key: aliasMigrationMarker}).First(&existingMarker).Error
	if markerErr != nil && !errors.Is(markerErr, gorm.ErrRecordNotFound) {
		return markerErr
	}
	modelAliases := map[string]string{}
	groupAliases := map[string]string{}
	if errors.Is(markerErr, gorm.ErrRecordNotFound) {
		setting := model_health_setting.GetSetting()
		modelAliases = make(map[string]string, len(setting.PublicModels))
		groupAliases = make(map[string]string, len(setting.PublicGroups))
		for _, item := range setting.PublicModels {
			if name, alias := strings.TrimSpace(item.Name), strings.TrimSpace(item.Alias); name != "" && alias != "" {
				modelAliases[name] = alias
			}
		}
		for _, item := range setting.PublicGroups {
			if name, alias := strings.TrimSpace(item.Name), strings.TrimSpace(item.Alias); name != "" && alias != "" {
				groupAliases[name] = alias
			}
		}
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		now := common.GetTimestamp()
		var marker Option
		markerErr := tx.Where(&Option{Key: aliasMigrationMarker}).First(&marker).Error
		if markerErr != nil && !errors.Is(markerErr, gorm.ErrRecordNotFound) {
			return markerErr
		}
		migrateAliases := errors.Is(markerErr, gorm.ErrRecordNotFound)
		for i := range targets {
			target := &targets[i]
			changed := false
			if strings.TrimSpace(target.SamplingMode) == "" && target.SamplesPerRun == 0 && target.MinimumSuccesses == 0 {
				if err := tx.Model(&ModelHealthTarget{}).Where("id = ?", target.ID).Updates(map[string]any{
					"sampling_mode":          ModelHealthSamplingFixed,
					"samples_per_run":        1,
					"minimum_successes":      1,
					"sample_spacing_seconds": model_health_setting.DefaultSampleSpacingSeconds,
				}).Error; err != nil {
					return err
				}
				target.SamplingMode = ModelHealthSamplingFixed
				target.SamplesPerRun = 1
				target.MinimumSuccesses = 1
				target.SampleSpacingSeconds = model_health_setting.DefaultSampleSpacingSeconds
			}
			if target.Mode == ModelHealthModeLocal && target.TokenID != nil && *target.TokenID > 0 {
				var token Token
				if err := tx.Select("id", "user_id").First(&token, *target.TokenID).Error; err == nil {
					mark := ModelHealthProbeToken{
						TokenID: token.Id, MarkedBy: token.UserId, CreatedAt: now, UpdatedAt: now,
					}
					if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&mark).Error; err != nil {
						return err
					}
				} else if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}

			if !migrateAliases {
				continue
			}
			groups, groupErr := target.ObservedGroups()
			if groupErr != nil {
				common.SysLog(fmt.Sprintf("skip invalid model health target %d group migration: %v", target.ID, groupErr))
				continue
			}
			if strings.TrimSpace(target.PublicGroupAlias) == "" {
				for _, group := range groups {
					if alias := groupAliases[group]; alias != "" {
						target.PublicGroupAlias = alias
						changed = true
						break
					}
				}
			}
			models, modelErr := target.Models()
			if modelErr != nil {
				common.SysLog(fmt.Sprintf("skip invalid model health target %d model alias migration: %v", target.ID, modelErr))
				continue
			}
			for index := range models {
				if strings.TrimSpace(models[index].PublicAlias) != "" {
					continue
				}
				if alias := modelAliases[models[index].Name]; alias != "" {
					models[index].PublicAlias = alias
					changed = true
				}
			}
			if !changed {
				continue
			}
			if err := target.SetModels(models); err != nil {
				return err
			}
			target.UpdatedAt = now
			if err := tx.Model(&ModelHealthTarget{}).Where("id = ?", target.ID).Updates(map[string]any{
				"models": target.ModelsJSON, "public_group_alias": target.PublicGroupAlias, "updated_at": now,
			}).Error; err != nil {
				return err
			}
		}
		if migrateAliases {
			marker = Option{Key: aliasMigrationMarker, Value: "true"}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
