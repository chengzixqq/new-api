package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/model_health_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/google/uuid"
)

var ErrModelHealthCredentialsNeedReplacement = errors.New("model health credentials must be replaced")

type ModelHealthTargetRunResult struct {
	TargetID  int64                         `json:"target_id"`
	CheckedAt int64                         `json:"checked_at"`
	Status    string                        `json:"status"`
	Results   []ModelHealthProbeBatchResult `json:"results"`
}

type ModelHealthSamplingConfig struct {
	Mode             string
	PlannedAttempts  int
	MinimumSuccesses int
	Spacing          time.Duration
}

type ModelHealthProbeBatchResult struct {
	Model           string                          `json:"model"`
	Status          string                          `json:"status"`
	BatchStatus     string                          `json:"batch_status"`
	Availability    float64                         `json:"availability"`
	PlannedAttempts int                             `json:"planned_attempts"`
	ActualAttempts  int                             `json:"actual_attempts"`
	SuccessCount    int                             `json:"success_count"`
	LatencyMs       int64                           `json:"latency_ms"`
	HTTPStatus      int                             `json:"http_status"`
	ErrorClass      string                          `json:"error_class,omitempty"`
	CheckedAt       int64                           `json:"checked_at"`
	Attempts        []ModelHealthProbeAttemptResult `json:"attempts"`
	attemptResults  []ModelHealthProbeResult
}

type ModelHealthProbeAttemptResult struct {
	AttemptIndex int    `json:"attempt_index"`
	Status       string `json:"status"`
	LatencyMs    int64  `json:"latency_ms"`
	HTTPStatus   int    `json:"http_status"`
	ErrorClass   string `json:"error_class,omitempty"`
	CheckedAt    int64  `json:"checked_at"`
}

type modelHealthProbeWaitFunc func(context.Context, time.Duration) error

func modelHealthSamplingConfig(target *model.ModelHealthTarget, enabled bool) ModelHealthSamplingConfig {
	config := ModelHealthSamplingConfig{
		Mode: model.ModelHealthSamplingFixed, PlannedAttempts: 1, MinimumSuccesses: 1,
		Spacing: time.Duration(model_health_setting.DefaultSampleSpacingSeconds) * time.Second,
	}
	if target == nil || !enabled {
		return config
	}
	if target.SamplingMode == model.ModelHealthSamplingFixed || target.SamplingMode == model.ModelHealthSamplingConfirmOnFailure {
		config.Mode = target.SamplingMode
	}
	if target.SamplesPerRun >= 1 && target.SamplesPerRun <= 5 {
		config.PlannedAttempts = target.SamplesPerRun
	}
	if target.MinimumSuccesses >= 1 && target.MinimumSuccesses <= config.PlannedAttempts {
		config.MinimumSuccesses = target.MinimumSuccesses
	}
	if target.SampleSpacingSeconds >= 1 && target.SampleSpacingSeconds <= 30 {
		config.Spacing = time.Duration(target.SampleSpacingSeconds) * time.Second
	}
	return normalizeModelHealthSamplingConfig(config)
}

func normalizeModelHealthSamplingConfig(config ModelHealthSamplingConfig) ModelHealthSamplingConfig {
	if config.Mode != model.ModelHealthSamplingFixed && config.Mode != model.ModelHealthSamplingConfirmOnFailure {
		config.Mode = model.ModelHealthSamplingFixed
	}
	if config.PlannedAttempts < 1 || config.PlannedAttempts > 5 {
		config.PlannedAttempts = 1
	}
	if config.MinimumSuccesses < 1 || config.MinimumSuccesses > config.PlannedAttempts {
		config.MinimumSuccesses = 1
	}
	if config.PlannedAttempts == 1 {
		config.MinimumSuccesses = 1
	}
	if config.Spacing < time.Second || config.Spacing > 30*time.Second {
		config.Spacing = time.Duration(model_health_setting.DefaultSampleSpacingSeconds) * time.Second
	}
	return config
}

func waitModelHealthProbeSpacing(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type ModelHealthProbeLimiter struct {
	mu      sync.Mutex
	limit   int
	active  int
	changed chan struct{}
}

func NewModelHealthProbeLimiter(concurrency int) *ModelHealthProbeLimiter {
	return &ModelHealthProbeLimiter{limit: normalizeModelHealthProbeConcurrency(concurrency), changed: make(chan struct{})}
}

var sharedModelHealthProbeLimiter = NewModelHealthProbeLimiter(model_health_setting.DefaultConcurrency)

func SharedModelHealthProbeLimiter() *ModelHealthProbeLimiter {
	sharedModelHealthProbeLimiter.setConcurrency(model_health_setting.GetSetting().Concurrency)
	return sharedModelHealthProbeLimiter
}

func normalizeModelHealthProbeConcurrency(concurrency int) int {
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 32 {
		concurrency = 32
	}
	return concurrency
}

func (limiter *ModelHealthProbeLimiter) setConcurrency(concurrency int) {
	if limiter == nil {
		return
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	concurrency = normalizeModelHealthProbeConcurrency(concurrency)
	if limiter.limit == concurrency {
		return
	}
	limiter.limit = concurrency
	limiter.notifyLocked()
}

func (limiter *ModelHealthProbeLimiter) acquire(ctx context.Context) bool {
	if limiter == nil {
		return true
	}
	for {
		limiter.mu.Lock()
		if limiter.active < limiter.limit {
			limiter.active++
			limiter.mu.Unlock()
			return true
		}
		changed := limiter.changed
		limiter.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return false
		}
	}
}

func (limiter *ModelHealthProbeLimiter) release() {
	if limiter == nil {
		return
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.active == 0 {
		return
	}
	limiter.active--
	limiter.notifyLocked()
}

func (limiter *ModelHealthProbeLimiter) notifyLocked() {
	close(limiter.changed)
	limiter.changed = make(chan struct{})
}

func UpdateModelHealthTargetCredentials(target *model.ModelHealthTarget, endpoint, apiKey string) error {
	if target == nil {
		return errors.New("model health target is nil")
	}
	endpoint = strings.TrimSpace(endpoint)
	apiKey = strings.TrimSpace(apiKey)
	if len(endpoint) > 2048 {
		return errors.New("upstream endpoint is too long")
	}
	if len(apiKey) > modelHealthMaxSecretBytes {
		return errors.New("upstream API key is too long")
	}
	if endpoint != "" {
		if err := ValidateModelHealthUpstreamURL(endpoint); err != nil {
			return err
		}
		encrypted, err := EncryptModelHealthSecret(endpoint)
		if err != nil {
			return err
		}
		target.EndpointEncrypted = encrypted
	}
	if apiKey != "" {
		encrypted, err := EncryptModelHealthSecret(apiKey)
		if err != nil {
			return err
		}
		target.APIKeyEncrypted = encrypted
		target.CredentialFingerprint = ModelHealthCredentialFingerprint(apiKey)
	}
	if target.EndpointEncrypted == "" || target.APIKeyEncrypted == "" {
		return errors.New("upstream endpoint and API key are required")
	}
	return nil
}

func ModelHealthTargetCredentialMetadata(target *model.ModelHealthTarget) (maskedEndpoint string, hasKey bool, fingerprint string, err error) {
	if target == nil || target.Mode != model.ModelHealthModeUpstream {
		return "", false, "", nil
	}
	endpoint, err := DecryptModelHealthSecret(target.EndpointEncrypted)
	if err != nil {
		return "***", target.APIKeyEncrypted != "", target.CredentialFingerprint, ErrModelHealthCredentialsNeedReplacement
	}
	if target.APIKeyEncrypted == "" {
		return MaskModelHealthEndpoint(endpoint), false, target.CredentialFingerprint, ErrModelHealthCredentialsNeedReplacement
	}
	if _, err := DecryptModelHealthSecret(target.APIKeyEncrypted); err != nil {
		return MaskModelHealthEndpoint(endpoint), true, target.CredentialFingerprint, ErrModelHealthCredentialsNeedReplacement
	}
	return MaskModelHealthEndpoint(endpoint), target.APIKeyEncrypted != "", target.CredentialFingerprint, nil
}

func RunModelHealthTarget(ctx context.Context, target *model.ModelHealthTarget) (ModelHealthTargetRunResult, error) {
	limiter := SharedModelHealthProbeLimiter()
	return runModelHealthTargetWithLimiter(ctx, target, newModelHealthProbeExecutor(), limiter)
}

func runModelHealthTarget(ctx context.Context, target *model.ModelHealthTarget, executor *modelHealthProbeExecutor) (ModelHealthTargetRunResult, error) {
	limiter := SharedModelHealthProbeLimiter()
	return runModelHealthTargetWithLimiter(ctx, target, executor, limiter)
}

func RunModelHealthTargetWithLimiter(ctx context.Context, target *model.ModelHealthTarget, limiter *ModelHealthProbeLimiter) (ModelHealthTargetRunResult, error) {
	return runModelHealthTargetWithLimiter(ctx, target, newModelHealthProbeExecutor(), limiter)
}

func runModelHealthTargetWithLimiter(ctx context.Context, target *model.ModelHealthTarget, executor *modelHealthProbeExecutor, limiter *ModelHealthProbeLimiter) (ModelHealthTargetRunResult, error) {
	return runModelHealthTargetWithLimiterAndWait(ctx, target, executor, limiter, waitModelHealthProbeSpacing)
}

func runModelHealthTargetWithLimiterAndWait(ctx context.Context, target *model.ModelHealthTarget, executor *modelHealthProbeExecutor, limiter *ModelHealthProbeLimiter, waitFn modelHealthProbeWaitFunc) (ModelHealthTargetRunResult, error) {
	result := ModelHealthTargetRunResult{}
	if target == nil || target.ID <= 0 {
		return result, errors.New("invalid model health target")
	}
	result.TargetID = target.ID
	if !target.Enabled {
		return result, errors.New("model health target is disabled")
	}
	targetModels, err := target.Models()
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	setting := model_health_setting.GetSetting()
	sampling := modelHealthSamplingConfig(target, setting.MultiSampleEnabled)
	probeRunID := uuid.NewString()
	runStartedAt := executor.now().Unix()

	baseURL := ""
	apiKey := ""
	switch target.Mode {
	case model.ModelHealthModeLocal:
		if target.TokenID == nil {
			return recordModelHealthCredentialFailure(target, targetModels, executor.now(), probeRunID, runStartedAt, sampling)
		}
		token, err := model.GetTokenById(*target.TokenID)
		if err != nil {
			return recordModelHealthCredentialFailure(target, targetModels, executor.now(), probeRunID, runStartedAt, sampling)
		}
		marked, err := model.IsModelHealthProbeTokenMarkedForOwner(token.Id, token.UserId)
		if err != nil || !marked {
			return recordModelHealthCredentialFailure(target, targetModels, executor.now(), probeRunID, runStartedAt, sampling)
		}
		baseURL = strings.TrimSpace(system_setting.ServerAddress)
		apiKey = token.Key
	case model.ModelHealthModeUpstream:
		baseURL, err = DecryptModelHealthSecret(target.EndpointEncrypted)
		if err == nil {
			apiKey, err = DecryptModelHealthSecret(target.APIKeyEncrypted)
		}
		if err != nil {
			if disableErr := model.DisableModelHealthTarget(target.ID); disableErr != nil {
				return result, fmt.Errorf("%w: failed to disable target", ErrModelHealthCredentialsNeedReplacement)
			}
			return result, ErrModelHealthCredentialsNeedReplacement
		}
	default:
		return result, errors.New("unsupported model health target mode")
	}

	results := make([]ModelHealthProbeBatchResult, len(targetModels))
	errorsByModel := make([]error, len(targetModels))
	var waitGroup sync.WaitGroup
	for index, targetModel := range targetModels {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		waitGroup.Add(1)
		go func(index int, targetModel model.ModelHealthTargetModel) {
			defer waitGroup.Done()
			batch, probeErr := executeModelHealthProbeBatch(ctx, executor, limiter, ModelHealthProbeConfig{
				Protocol: target.Protocol,
				BaseURL:  baseURL,
				APIKey:   apiKey,
				Model:    targetModel.Name,
				Timeout:  time.Duration(target.TimeoutSeconds) * time.Second,
				Local:    target.Mode == model.ModelHealthModeLocal,
			}, sampling, waitFn)
			results[index], errorsByModel[index] = batch, probeErr
		}(index, targetModel)
	}
	waitGroup.Wait()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	for _, probeErr := range errorsByModel {
		if probeErr != nil {
			return result, probeErr
		}
	}

	histories := make([]model.ModelHealthHistory, 0, len(results)*sampling.PlannedAttempts)
	for _, batch := range results {
		if batch.CheckedAt > result.CheckedAt {
			result.CheckedAt = batch.CheckedAt
		}
		for attemptIndex, attempt := range batch.attemptResults {
			histories = append(histories, model.ModelHealthHistory{
				TargetID: target.ID, ModelName: attempt.Model, ProbeRunID: probeRunID,
				RunStartedAt: runStartedAt, AttemptIndex: attemptIndex + 1,
				SamplingMode: sampling.Mode, PlannedAttempts: sampling.PlannedAttempts,
				MinimumSuccesses: sampling.MinimumSuccesses, Status: attempt.Status,
				LatencyMs: attempt.LatencyMs, HTTPStatus: attempt.HTTPStatus,
				ErrorClass: attempt.ErrorClass, CheckedAt: attempt.CheckedAt,
			})
		}
	}
	if result.CheckedAt == 0 {
		result.CheckedAt = executor.now().Unix()
	}
	if err := model.RecordModelHealthTargetRun(target.ID, result.CheckedAt, histories); err != nil {
		return result, err
	}
	result.Results = results
	result.Status = RollupRequiredModelHealthProbeBatches(targetModels, results)
	return result, nil
}

func recordModelHealthCredentialFailure(target *model.ModelHealthTarget, targetModels []model.ModelHealthTargetModel, checkedAt time.Time, probeRunID string, runStartedAt int64, sampling ModelHealthSamplingConfig) (ModelHealthTargetRunResult, error) {
	result := ModelHealthTargetRunResult{
		TargetID: target.ID, CheckedAt: checkedAt.Unix(),
		Results: make([]ModelHealthProbeBatchResult, 0, len(targetModels)),
	}
	histories := make([]model.ModelHealthHistory, 0, len(targetModels))
	for _, targetModel := range targetModels {
		probe := ModelHealthProbeResult{
			Model: targetModel.Name, Status: model.ModelHealthProbeAuthFailure,
			ErrorClass: ModelHealthErrorCredential, CheckedAt: result.CheckedAt,
		}
		batch := finalizeModelHealthProbeBatch(targetModel.Name, []ModelHealthProbeResult{probe}, sampling, true)
		result.Results = append(result.Results, batch)
		histories = append(histories, model.ModelHealthHistory{
			TargetID: target.ID, ModelName: targetModel.Name, ProbeRunID: probeRunID,
			RunStartedAt: runStartedAt, AttemptIndex: 1, SamplingMode: sampling.Mode,
			PlannedAttempts: sampling.PlannedAttempts, MinimumSuccesses: sampling.MinimumSuccesses,
			Status: probe.Status, ErrorClass: probe.ErrorClass, CheckedAt: result.CheckedAt,
		})
	}
	if err := model.RecordModelHealthTargetRun(target.ID, result.CheckedAt, histories); err != nil {
		return result, err
	}
	result.Status = RollupRequiredModelHealthProbeBatches(targetModels, result.Results)
	return result, nil
}

func ExecuteModelHealthProbeBatch(ctx context.Context, config ModelHealthProbeConfig, sampling ModelHealthSamplingConfig) (ModelHealthProbeBatchResult, error) {
	executor := newModelHealthProbeExecutor()
	limiter := SharedModelHealthProbeLimiter()
	return executeModelHealthProbeBatch(ctx, executor, limiter, config, sampling, waitModelHealthProbeSpacing)
}

func executeModelHealthProbeBatch(ctx context.Context, executor *modelHealthProbeExecutor, limiter *ModelHealthProbeLimiter, config ModelHealthProbeConfig, sampling ModelHealthSamplingConfig, wait modelHealthProbeWaitFunc) (ModelHealthProbeBatchResult, error) {
	sampling = normalizeModelHealthSamplingConfig(sampling)
	if wait == nil {
		wait = waitModelHealthProbeSpacing
	}
	attempts := make([]ModelHealthProbeResult, 0, sampling.PlannedAttempts)
	deterministicStop := false
	for attemptIndex := 0; attemptIndex < sampling.PlannedAttempts; attemptIndex++ {
		if !limiter.acquire(ctx) {
			return ModelHealthProbeBatchResult{}, ctx.Err()
		}
		probeResult, probeErr := executor.Probe(ctx, config)
		limiter.release()
		if err := ctx.Err(); err != nil {
			return ModelHealthProbeBatchResult{}, err
		}
		if probeErr != nil {
			probeResult = ModelHealthProbeResult{
				Model: config.Model, Status: model.ModelHealthProbeFailure,
				ErrorClass: ModelHealthErrorClient, CheckedAt: executor.now().Unix(),
			}
		}
		attempts = append(attempts, probeResult)
		deterministicStop = isDeterministicModelHealthProbeResult(probeResult)
		if deterministicStop || (sampling.Mode == model.ModelHealthSamplingConfirmOnFailure && attemptIndex == 0 && probeResult.Status == model.ModelHealthProbeSuccess) {
			break
		}
		if attemptIndex+1 < sampling.PlannedAttempts {
			if err := wait(ctx, sampling.Spacing); err != nil {
				return ModelHealthProbeBatchResult{}, err
			}
		}
	}
	return finalizeModelHealthProbeBatch(config.Model, attempts, sampling, deterministicStop), nil
}

func finalizeModelHealthProbeBatch(modelName string, attempts []ModelHealthProbeResult, sampling ModelHealthSamplingConfig, deterministicStop bool) ModelHealthProbeBatchResult {
	batch := ModelHealthProbeBatchResult{
		Model: modelName, PlannedAttempts: sampling.PlannedAttempts, ActualAttempts: len(attempts),
		Attempts: make([]ModelHealthProbeAttemptResult, 0, len(attempts)), attemptResults: attempts,
	}
	for attemptIndex, attempt := range attempts {
		batch.Attempts = append(batch.Attempts, ModelHealthProbeAttemptResult{
			AttemptIndex: attemptIndex + 1, Status: attempt.Status, LatencyMs: attempt.LatencyMs,
			HTTPStatus: attempt.HTTPStatus, ErrorClass: attempt.ErrorClass, CheckedAt: attempt.CheckedAt,
		})
		if attempt.Status == model.ModelHealthProbeSuccess {
			batch.SuccessCount++
		}
		if attempt.CheckedAt > batch.CheckedAt {
			batch.CheckedAt = attempt.CheckedAt
		}
	}
	if len(attempts) > 0 {
		latest := attempts[len(attempts)-1]
		batch.Status, batch.LatencyMs, batch.HTTPStatus = latest.Status, latest.LatencyMs, latest.HTTPStatus
		batch.ErrorClass = latest.ErrorClass
		batch.Availability = math.Round(float64(batch.SuccessCount)/float64(len(attempts))*10000) / 100
	}
	switch {
	case len(attempts) > 0 && batch.SuccessCount == len(attempts):
		batch.BatchStatus = ModelHealthStatusNormal
		batch.Status = model.ModelHealthProbeSuccess
	case deterministicStop || batch.SuccessCount < sampling.MinimumSuccesses:
		batch.BatchStatus = ModelHealthStatusAbnormal
	default:
		batch.BatchStatus = ModelHealthStatusFluctuating
		batch.Status = model.ModelHealthProbeSuccess
	}
	return batch
}

func isDeterministicModelHealthProbeResult(result ModelHealthProbeResult) bool {
	switch result.ErrorClass {
	case ModelHealthErrorAuthentication, ModelHealthErrorRateLimited, ModelHealthErrorClient,
		ModelHealthErrorRedirect, ModelHealthErrorCredential:
		return true
	default:
		return false
	}
}

// RollupRequiredModelHealthProbeResults makes required models authoritative:
// the target takes their worst status, while optional models remain visible in
// Results without affecting the rollup.
func RollupRequiredModelHealthProbeResults(models []model.ModelHealthTargetModel, results []ModelHealthProbeResult) string {
	resultByModel := make(map[string]ModelHealthProbeResult, len(results))
	for _, result := range results {
		resultByModel[result.Model] = result
	}
	statuses := make([]string, 0)
	for _, targetModel := range models {
		if !targetModel.Required {
			continue
		}
		result, ok := resultByModel[targetModel.Name]
		if !ok {
			statuses = append(statuses, ModelHealthStatusIdle)
			continue
		}
		if result.Status == model.ModelHealthProbeSuccess {
			statuses = append(statuses, ModelHealthStatusNormal)
		} else {
			statuses = append(statuses, ModelHealthStatusAbnormal)
		}
	}
	return WorstModelHealthStatus(statuses)
}

func RollupRequiredModelHealthProbeBatches(models []model.ModelHealthTargetModel, results []ModelHealthProbeBatchResult) string {
	resultByModel := make(map[string]ModelHealthProbeBatchResult, len(results))
	for _, result := range results {
		resultByModel[result.Model] = result
	}
	statuses := make([]string, 0, len(models))
	for _, targetModel := range models {
		if !targetModel.Required {
			continue
		}
		result, ok := resultByModel[targetModel.Name]
		if !ok || result.BatchStatus == "" {
			statuses = append(statuses, ModelHealthStatusIdle)
			continue
		}
		statuses = append(statuses, result.BatchStatus)
	}
	return WorstModelHealthStatus(statuses)
}
