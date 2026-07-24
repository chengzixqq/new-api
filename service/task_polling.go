package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/samber/lo"
)

// TaskPollingAdaptor 定义轮询所需的最小适配器接口，避免 service -> relay 的循环依赖
type TaskPollingAdaptor interface {
	Init(info *relaycommon.RelayInfo)
	FetchTask(ctx context.Context, baseURL string, key string, body map[string]any, proxy string) (*http.Response, error)
	ParseTaskResult(body []byte) (*relaycommon.TaskInfo, error)
	// AdjustBillingOnComplete 在任务到达终态（成功/失败）时由轮询循环调用。
	// 返回正数触发差额结算（补扣/退还），返回 0 保持预扣费金额不变。
	AdjustBillingOnComplete(task *model.Task, taskResult *relaycommon.TaskInfo) int
}

// TaskPollingContextResultAdaptor is implemented by providers that need an
// additional upstream call while translating a polling response. The follow-up
// request then inherits the same cancellation and deadline as FetchTask.
type TaskPollingContextResultAdaptor interface {
	ParseTaskResultWithContext(ctx context.Context, body []byte) (*relaycommon.TaskInfo, error)
}

var taskFetchTimeout = 60 * time.Second

// GetTaskAdaptorFunc 由 main 包注入，用于获取指定平台的任务适配器。
// 打破 service -> relay -> relay/channel -> service 的循环依赖。
var GetTaskAdaptorFunc func(platform constant.TaskPlatform) TaskPollingAdaptor

// sweepTimedOutTasks 在主轮询之前独立清理超时任务。
// 每次最多处理 100 条，剩余的下个周期继续处理。
// 使用 per-task CAS (UpdateWithStatus) 防止覆盖被正常轮询已推进的任务。
func sweepTimedOutTasks(ctx context.Context) {
	if constant.TaskTimeoutMinutes <= 0 {
		return
	}
	cutoff := time.Now().Unix() - int64(constant.TaskTimeoutMinutes)*60
	tasks := model.GetTimedOutUnfinishedTasks(cutoff, 100)
	if len(tasks) == 0 {
		return
	}

	reason := fmt.Sprintf("任务超时（%d分钟）", constant.TaskTimeoutMinutes)
	now := time.Now().Unix()
	timedOutCount := 0

	for _, task := range tasks {
		task.FinishTime = now
		if err := RefundTaskQuota(ctx, task, reason); err != nil {
			continue
		}
		timedOutCount++
	}

	if timedOutCount > 0 {
		logger.LogInfo(ctx, fmt.Sprintf("sweepTimedOutTasks: timed out %d tasks", timedOutCount))
	}
}

// TaskPollSummary is the result recorded on an async_task_poll system task row,
// summarizing one polling pass.
type TaskPollSummary struct {
	UnfinishedTasks  int `json:"unfinished_tasks"`
	PlatformsScanned int `json:"platforms_scanned"`
	NullTasksFailed  int `json:"null_tasks_failed"`
}

// RunTaskPollingOnce performs one async-task (Suno/video) polling pass
// synchronously. It honors ctx cancellation (the system-task runner cancels it
// when the lease is lost) and, when report is non-nil, reports progress as
// (processedPlatforms, totalPlatforms). It returns immediately if the task
// adaptor factory has not been wired yet, to avoid a nil call during startup.
func RunTaskPollingOnce(ctx context.Context, report func(processed, total int)) TaskPollSummary {
	summary := TaskPollSummary{}
	if GetTaskAdaptorFunc == nil {
		return summary
	}
	if ctx == nil {
		ctx = context.Background()
	}

	common.SysLog("任务进度轮询开始")
	sweepTimedOutTasks(ctx)
	allTasks := model.GetAllUnFinishSyncTasks(constant.TaskQueryLimit)
	summary.UnfinishedTasks = len(allTasks)
	platformTask := make(map[constant.TaskPlatform][]*model.Task)
	for _, t := range allTasks {
		platformTask[t.Platform] = append(platformTask[t.Platform], t)
	}

	totalPlatforms := len(platformTask)
	processedPlatforms := 0
	for platform, tasks := range platformTask {
		if ctx.Err() != nil {
			break
		}
		if report != nil {
			report(processedPlatforms, totalPlatforms)
		}
		processedPlatforms++
		if len(tasks) == 0 {
			continue
		}
		summary.PlatformsScanned++
		taskChannelM := make(map[int][]*model.Task)
		nullTasks := make([]*model.Task, 0)
		for _, task := range tasks {
			upstreamID := task.GetUpstreamTaskID()
			if upstreamID == "" {
				// 统计失败的未完成任务
				nullTasks = append(nullTasks, task)
				continue
			}
			taskChannelM[task.ChannelId] = append(taskChannelM[task.ChannelId], task)
		}
		if len(nullTasks) > 0 {
			for _, task := range nullTasks {
				if err := RefundTaskQuota(ctx, task, "上游任务 ID 为空"); err != nil {
					logger.LogError(ctx, fmt.Sprintf("Finalize null upstream task %d failed: %v", task.ID, err))
					continue
				}
				summary.NullTasksFailed++
			}
		}
		if len(taskChannelM) == 0 {
			continue
		}

		DispatchPlatformUpdate(ctx, platform, taskChannelM)
	}
	if report != nil && ctx.Err() == nil {
		report(totalPlatforms, totalPlatforms)
	}
	common.SysLog("任务进度轮询完成")
	return summary
}

// DispatchPlatformUpdate 按平台分发轮询更新
func DispatchPlatformUpdate(ctx context.Context, platform constant.TaskPlatform, taskChannelM map[int][]*model.Task) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch platform {
	case constant.TaskPlatformMidjourney:
		// MJ 轮询由其自身处理，这里预留入口
	case constant.TaskPlatformSuno:
		_ = UpdateSunoTasks(ctx, taskChannelM)
	default:
		if err := UpdateVideoTasks(ctx, platform, taskChannelM); err != nil {
			common.SysLog(fmt.Sprintf("UpdateVideoTasks fail: %s", err))
		}
	}
}

// UpdateSunoTasks 按渠道更新所有 Suno 任务
func UpdateSunoTasks(ctx context.Context, taskChannelM map[int][]*model.Task) error {
	for channelId, tasks := range taskChannelM {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := updateSunoTasks(ctx, channelId, tasks)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("渠道 #%d 更新异步任务失败: %s", channelId, err.Error()))
		}
	}
	return nil
}

func updateSunoTasks(ctx context.Context, channelId int, tasks []*model.Task) error {
	taskIds := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if task != nil {
			taskIds = append(taskIds, task.GetUpstreamTaskID())
		}
	}
	logger.LogInfo(ctx, fmt.Sprintf("渠道 #%d 未完成的任务有: %d", channelId, len(taskIds)))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(tasks) == 0 {
		return nil
	}
	ch, err := model.CacheGetChannel(channelId)
	if err != nil {
		common.SysLog(fmt.Sprintf("CacheGetChannel: %v", err))
		reason := fmt.Sprintf("获取渠道信息失败，请联系管理员，渠道ID：%d", channelId)
		for _, task := range tasks {
			if task != nil {
				if refundErr := RefundTaskQuota(ctx, task, reason); refundErr != nil {
					logger.LogError(ctx, fmt.Sprintf("Finalize Suno task %s after channel loss failed: %v", task.TaskID, refundErr))
				}
			}
		}
		return err
	}
	adaptor := GetTaskAdaptorFunc(constant.TaskPlatformSuno)
	if adaptor == nil {
		return errors.New("adaptor not found")
	}
	proxy := ch.GetSetting().Proxy
	fetchCtx, cancelFetch := context.WithTimeout(ctx, taskFetchTimeout)
	fetchCtx = WithChannelUpstreamHTTPPolicy(fetchCtx, ch.Id, ch.GetOtherSettings())
	resp, err := adaptor.FetchTask(fetchCtx, *ch.BaseURL, ch.Key, map[string]any{
		"ids": taskIds,
	}, proxy)
	if err != nil {
		cancelFetch()
		common.SysLog(fmt.Sprintf("Get Task Do req error: %v", err))
		return err
	}
	if resp == nil || resp.Body == nil {
		cancelFetch()
		return errors.New("Get Task returned an empty response")
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancelFetch()
		logger.LogError(ctx, fmt.Sprintf("Get Task status code: %d", resp.StatusCode))
		return fmt.Errorf("Get Task status code: %d", resp.StatusCode)
	}
	responseBody, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	cancelFetch()
	if err != nil {
		common.SysLog(fmt.Sprintf("Get Suno Task parse body error: %v", err))
		return err
	}
	var responseItems dto.TaskResponse[[]dto.SunoDataResponse]
	err = common.Unmarshal(responseBody, &responseItems)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("Get Suno Task parse body error2: %v, body: %s", err, string(responseBody)))
		return err
	}
	if !responseItems.IsSuccess() {
		common.SysLog(fmt.Sprintf("渠道 #%d 未完成的任务有: %d, 成功获取到任务数: %s", channelId, len(taskIds), string(responseBody)))
		return err
	}

	for _, responseItem := range responseItems.Data {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var task *model.Task
		for _, candidate := range tasks {
			if candidate != nil && candidate.GetUpstreamTaskID() == responseItem.TaskID {
				task = candidate
				break
			}
		}
		if task == nil {
			logger.LogWarn(ctx, fmt.Sprintf("Suno task response ignored: unknown task_id=%s", responseItem.TaskID))
			continue
		}
		if !taskNeedsUpdate(task, responseItem) {
			continue
		}

		snap := task.Snapshot()
		task.Status = lo.If(model.TaskStatus(responseItem.Status) != "", model.TaskStatus(responseItem.Status)).Else(task.Status)
		task.FailReason = lo.If(responseItem.FailReason != "", responseItem.FailReason).Else(task.FailReason)
		task.SubmitTime = lo.If(responseItem.SubmitTime != 0, responseItem.SubmitTime).Else(task.SubmitTime)
		task.StartTime = lo.If(responseItem.StartTime != 0, responseItem.StartTime).Else(task.StartTime)
		task.FinishTime = lo.If(responseItem.FinishTime != 0, responseItem.FinishTime).Else(task.FinishTime)
		task.Data = responseItem.Data
		terminalFailure := responseItem.FailReason != "" || task.Status == model.TaskStatusFailure
		if terminalFailure {
			logger.LogInfo(ctx, task.TaskID+" 构建失败，"+task.FailReason)
			if err := RefundTaskQuota(ctx, task, task.FailReason); err != nil {
				logger.LogError(ctx, fmt.Sprintf("Finalize Suno task %s refund failed: %v", task.TaskID, err))
			}
			continue
		}
		if responseItem.Status == model.TaskStatusSuccess {
			task.Progress = "100%"
		}
		if task.Status == model.TaskStatusSuccess {
			if err := RecalculateTaskQuota(ctx, task, task.Quota, "Suno任务完成"); err != nil {
				logger.LogError(ctx, fmt.Sprintf("Finalize Suno task %s settlement failed: %v", task.TaskID, err))
			}
			continue
		}

		if !snap.Equal(task.Snapshot()) {
			if _, err := task.UpdateWithStatus(snap.Status); err != nil {
				common.SysLog("UpdateSunoTask task error: " + err.Error())
			}
		}
	}
	return nil
}

// taskNeedsUpdate 检查 Suno 任务是否需要更新
func taskNeedsUpdate(oldTask *model.Task, newTask dto.SunoDataResponse) bool {
	if oldTask.SubmitTime != newTask.SubmitTime {
		return true
	}
	if oldTask.StartTime != newTask.StartTime {
		return true
	}
	if oldTask.FinishTime != newTask.FinishTime {
		return true
	}
	if string(oldTask.Status) != newTask.Status {
		return true
	}
	if oldTask.FailReason != newTask.FailReason {
		return true
	}

	if (oldTask.Status == model.TaskStatusFailure || oldTask.Status == model.TaskStatusSuccess) && oldTask.Progress != "100%" {
		return true
	}

	oldData, _ := common.Marshal(oldTask.Data)
	newData, _ := common.Marshal(newTask.Data)

	sort.Slice(oldData, func(i, j int) bool {
		return oldData[i] < oldData[j]
	})
	sort.Slice(newData, func(i, j int) bool {
		return newData[i] < newData[j]
	})

	if string(oldData) != string(newData) {
		return true
	}
	return false
}

// UpdateVideoTasks 按渠道更新所有视频任务
func UpdateVideoTasks(ctx context.Context, platform constant.TaskPlatform, taskChannelM map[int][]*model.Task) error {
	channelIDs := make([]int, 0, len(taskChannelM))
	for channelID := range taskChannelM {
		channelIDs = append(channelIDs, channelID)
	}
	sort.Ints(channelIDs)

	var wg sync.WaitGroup
	for _, channelId := range channelIDs {
		tasks := taskChannelM[channelId]
		if len(tasks) == 0 {
			continue
		}
		tasks = append([]*model.Task(nil), tasks...)

		wg.Add(1)
		gopool.Go(func() {
			defer wg.Done()
			if err := updateVideoTasks(ctx, platform, channelId, tasks); err != nil {
				logger.LogError(ctx, fmt.Sprintf("Channel #%d failed to update video async tasks: %s", channelId, err.Error()))
			}
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

const (
	defaultTaskPollingConcurrency = 4
	defaultTaskPollingInterval    = 250 * time.Millisecond
)

func resolveTaskPollingSchedule(settings dto.ChannelOtherSettings) (int, time.Duration) {
	concurrency := defaultTaskPollingConcurrency
	if settings.TaskPollingConcurrency != nil &&
		*settings.TaskPollingConcurrency >= dto.MinTaskPollingConcurrency &&
		*settings.TaskPollingConcurrency <= dto.MaxTaskPollingConcurrency {
		concurrency = *settings.TaskPollingConcurrency
	}

	interval := defaultTaskPollingInterval
	if settings.TaskPollingIntervalMs != nil {
		if *settings.TaskPollingIntervalMs >= dto.MinTaskPollingIntervalMs &&
			*settings.TaskPollingIntervalMs <= dto.MaxTaskPollingIntervalMs {
			interval = time.Duration(*settings.TaskPollingIntervalMs) * time.Millisecond
		}
	} else if settings.DisableTaskPollingSleep {
		interval = 0
	}
	return concurrency, interval
}

func updateVideoTasks(ctx context.Context, platform constant.TaskPlatform, channelId int, tasks []*model.Task) error {
	logger.LogInfo(ctx, fmt.Sprintf("Channel #%d pending video tasks: %d", channelId, len(tasks)))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(tasks) == 0 {
		return nil
	}
	cacheGetChannel, err := model.CacheGetChannel(channelId)
	if err != nil {
		reason := fmt.Sprintf("Failed to get channel info, channel ID: %d", channelId)
		for _, task := range tasks {
			if task != nil {
				if refundErr := RefundTaskQuota(ctx, task, reason); refundErr != nil {
					logger.LogError(ctx, fmt.Sprintf("Finalize video task %s after channel loss failed: %v", task.TaskID, refundErr))
				}
			}
		}
		return fmt.Errorf("CacheGetChannel failed: %w", err)
	}
	adaptor := GetTaskAdaptorFunc(platform)
	if adaptor == nil {
		return fmt.Errorf("video adaptor not found")
	}
	info := &relaycommon.RelayInfo{}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelId:            cacheGetChannel.Id,
		ChannelBaseUrl:       cacheGetChannel.GetBaseURL(),
		ChannelSetting:       cacheGetChannel.GetSetting(),
		ChannelOtherSettings: cacheGetChannel.GetOtherSettings(),
	}
	info.ApiKey = cacheGetChannel.Key
	adaptor.Init(info)
	concurrency, interval := resolveTaskPollingSchedule(cacheGetChannel.GetOtherSettings())
	workerSlots := make(chan struct{}, concurrency)
	var workers sync.WaitGroup
	var launchErr error
	launched := 0

launchLoop:
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if launched > 0 && interval > 0 {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				launchErr = ctx.Err()
				break launchLoop
			case <-timer.C:
			}
		}

		select {
		case workerSlots <- struct{}{}:
		case <-ctx.Done():
			launchErr = ctx.Err()
			break launchLoop
		}

		launched++
		task := task
		workers.Add(1)
		gopool.Go(func() {
			defer workers.Done()
			defer func() { <-workerSlots }()
			if err := updateVideoSingleTask(ctx, adaptor, cacheGetChannel, task); err != nil {
				logger.LogError(ctx, fmt.Sprintf("Failed to update video task %s: %s", task.GetUpstreamTaskID(), err.Error()))
			}
		})
	}
	workers.Wait()
	if launchErr != nil {
		return launchErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func updateVideoSingleTask(ctx context.Context, adaptor TaskPollingAdaptor, ch *model.Channel, task *model.Task) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	baseURL := constant.ChannelBaseURLs[ch.Type]
	if ch.GetBaseURL() != "" {
		baseURL = ch.GetBaseURL()
	}
	proxy := ch.GetSetting().Proxy

	if task == nil {
		return errors.New("task is nil")
	}
	taskId := task.GetUpstreamTaskID()
	key := ch.Key

	privateData := task.PrivateData
	if privateData.Key != "" {
		key = privateData.Key
	}
	fetchCtx, cancelFetch := context.WithTimeout(ctx, taskFetchTimeout)
	defer cancelFetch()
	fetchCtx = WithChannelUpstreamHTTPPolicy(fetchCtx, ch.Id, ch.GetOtherSettings())
	resp, err := adaptor.FetchTask(fetchCtx, baseURL, key, map[string]any{
		"task_id": taskId,
		"action":  task.Action,
	}, proxy)
	if err != nil {
		return fmt.Errorf("fetchTask failed for task %s: %w", taskId, err)
	}
	if resp == nil || resp.Body == nil {
		return fmt.Errorf("fetchTask returned an empty response for task %s", taskId)
	}
	responseBody, err := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if err != nil {
		return fmt.Errorf("readAll failed for task %s: %w", taskId, err)
	}
	if closeErr != nil {
		logger.LogWarn(ctx, fmt.Sprintf("close task response body %s failed: %v", taskId, closeErr))
	}

	logger.LogDebug(ctx, "updateVideoSingleTask response: %s", responseBody)

	snap := task.Snapshot()

	taskResult := &relaycommon.TaskInfo{}
	// try parse as New API response format
	var responseItems dto.TaskResponse[model.Task]
	if err = common.Unmarshal(responseBody, &responseItems); err == nil && responseItems.IsSuccess() {
		logger.LogDebug(ctx, "updateVideoSingleTask parsed as new api response format: %+v", responseItems)
		t := responseItems.Data
		taskResult.TaskID = t.TaskID
		taskResult.Status = string(t.Status)
		taskResult.Url = t.GetResultURL()
		taskResult.Progress = t.Progress
		taskResult.Reason = t.FailReason
	} else {
		if contextualAdaptor, ok := adaptor.(TaskPollingContextResultAdaptor); ok {
			taskResult, err = contextualAdaptor.ParseTaskResultWithContext(fetchCtx, responseBody)
		} else {
			taskResult, err = adaptor.ParseTaskResult(responseBody)
		}
		if err != nil {
			return fmt.Errorf("parseTaskResult failed for task %s: %w", taskId, err)
		}
	}
	if taskResult == nil {
		return fmt.Errorf("provider returned an empty task result for task %s", taskId)
	}
	if taskResult.TaskID != "" && taskResult.TaskID != taskId {
		return fmt.Errorf("provider returned task_id %q for requested task_id %q", taskResult.TaskID, taskId)
	}

	task.Data = redactVideoResponseBody(responseBody)

	logger.LogDebug(ctx, "updateVideoSingleTask taskResult: %+v", taskResult)

	now := time.Now().Unix()
	if taskResult.Status == "" {
		//taskResult = relaycommon.FailTaskInfo("upstream returned empty status")
		errorResult := &dto.GeneralErrorResponse{}
		if err = common.Unmarshal(responseBody, &errorResult); err == nil {
			openaiError := errorResult.TryToOpenAIError()
			if openaiError != nil {
				// 返回规范的 OpenAI 错误格式，提取错误信息，判断错误是否为任务失败
				if openaiError.Code == "429" {
					// 429 错误通常表示请求过多或速率限制，暂时不认为是任务失败，保持原状态等待下一轮轮询
					return nil
				}

				// 其他错误认为是任务失败，记录错误信息并更新任务状态
				taskResult = relaycommon.FailTaskInfo("upstream returned error")
			} else {
				// unknown error format, log original response
				logger.LogError(ctx, fmt.Sprintf("Task %s returned empty status with unrecognized error format, response: %s", taskId, string(responseBody)))
				taskResult = relaycommon.FailTaskInfo("upstream returned unrecognized message")
			}
		}
	}

	task.Status = model.TaskStatus(taskResult.Status)
	switch taskResult.Status {
	case model.TaskStatusSubmitted:
		task.Progress = taskcommon.ProgressSubmitted
	case model.TaskStatusQueued:
		task.Progress = taskcommon.ProgressQueued
	case model.TaskStatusInProgress:
		task.Progress = taskcommon.ProgressInProgress
		if task.StartTime == 0 {
			task.StartTime = now
		}
	case model.TaskStatusSuccess:
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		if strings.HasPrefix(taskResult.Url, "data:") {
			// data: URI (e.g. Vertex base64 encoded video) — keep in Data, not in ResultURL
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		} else if taskResult.Url != "" {
			// Direct upstream URL (e.g. Kling, Ali, Doubao, etc.)
			task.PrivateData.ResultURL = taskResult.Url
		} else {
			// No URL from adaptor — construct proxy URL using public task ID
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		}
	case model.TaskStatusFailure:
		logger.LogJson(ctx, fmt.Sprintf("Task %s failed", taskId), task)
		task.Status = model.TaskStatusFailure
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		task.FailReason = taskResult.Reason
		logger.LogInfo(ctx, fmt.Sprintf("Task %s failed: %s", task.TaskID, task.FailReason))
		taskResult.Progress = taskcommon.ProgressComplete
	default:
		return fmt.Errorf("unknown task status %s for task %s", taskResult.Status, task.TaskID)
	}
	if taskResult.Progress != "" {
		task.Progress = taskResult.Progress
	}

	isDone := task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure
	if !isDone && !snap.Equal(task.Snapshot()) {
		if _, err := task.UpdateWithStatus(snap.Status); err != nil {
			logger.LogError(ctx, fmt.Sprintf("Failed to update task %s: %s", task.TaskID, err.Error()))
		}
	} else {
		// No changes, skip update
		logger.LogDebug(ctx, "No update needed for task %s", task.TaskID)
	}

	if task.Status == model.TaskStatusSuccess {
		if err := settleTaskBillingOnComplete(ctx, adaptor, task, taskResult); err != nil {
			return fmt.Errorf("settle task %s: %w", task.TaskID, err)
		}
	}
	if task.Status == model.TaskStatusFailure {
		if err := RefundTaskQuota(ctx, task, task.FailReason); err != nil {
			return fmt.Errorf("refund task %s: %w", task.TaskID, err)
		}
	}

	return nil
}

func redactVideoResponseBody(body []byte) []byte {
	var m map[string]any
	if err := common.Unmarshal(body, &m); err != nil {
		return body
	}
	resp, _ := m["response"].(map[string]any)
	if resp != nil {
		delete(resp, "bytesBase64Encoded")
		if v, ok := resp["video"].(string); ok {
			resp["video"] = truncateBase64(v)
		}
		if vs, ok := resp["videos"].([]any); ok {
			for i := range vs {
				if vm, ok := vs[i].(map[string]any); ok {
					delete(vm, "bytesBase64Encoded")
				}
			}
		}
	}
	b, err := common.Marshal(m)
	if err != nil {
		return body
	}
	return b
}

func truncateBase64(s string) string {
	const maxKeep = 256
	if len(s) <= maxKeep {
		return s
	}
	return s[:maxKeep] + "..."
}

// settleTaskBillingOnComplete 任务完成时的统一计费调整。
// 优先级：1. adaptor.AdjustBillingOnComplete 返回正数 → 使用 adaptor 计算的额度
//
//  2. taskResult.TotalTokens > 0 → 按 token 重算
//  3. 都不满足 → 保持预扣额度不变
func settleTaskBillingOnComplete(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, taskResult *relaycommon.TaskInfo) error {
	// 0. 按次计费的任务不做差额结算
	if bc := task.PrivateData.BillingContext; bc != nil && bc.PerCallBilling {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 按次计费，跳过差额结算", task.TaskID))
		return RecalculateTaskQuota(ctx, task, task.Quota, "按次计费任务完成")
	}
	// 1. 优先让 adaptor 决定最终额度
	if actualQuota := adaptor.AdjustBillingOnComplete(task, taskResult); actualQuota > 0 {
		return RecalculateTaskQuota(ctx, task, actualQuota, "adaptor计费调整")
	}
	// 2. 回退到 token 重算
	if taskResult.TotalTokens > 0 {
		return RecalculateTaskQuotaByTokens(ctx, task, taskResult.TotalTokens)
	}
	// 3. 无调整，保持预扣额度
	return RecalculateTaskQuota(ctx, task, task.Quota, "无差额调整，保持预扣额度")
}
