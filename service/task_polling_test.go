package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type taskPollingFetchAdaptor struct {
	mu            sync.Mutex
	taskIDs       []string
	fetched       chan string
	blockTaskID   string
	blockStarted  chan struct{}
	releaseBlock  chan struct{}
	blockOnce     sync.Once
	bodyFactory   func(context.Context) io.ReadCloser
	responseByKey map[string]model.Task
}

type pollingContextReadCloser struct {
	ctx context.Context
}

func (r *pollingContextReadCloser) Read([]byte) (int, error) {
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func (r *pollingContextReadCloser) Close() error { return nil }

func (a *taskPollingFetchAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *taskPollingFetchAdaptor) FetchTask(ctx context.Context, _ string, key string, body map[string]any, _ string) (*http.Response, error) {
	taskID, _ := body["task_id"].(string)
	if taskID == a.blockTaskID && a.releaseBlock != nil {
		a.blockOnce.Do(func() {
			if a.blockStarted != nil {
				close(a.blockStarted)
			}
		})
		select {
		case <-a.releaseBlock:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	a.mu.Lock()
	a.taskIDs = append(a.taskIDs, taskID)
	a.mu.Unlock()
	if a.fetched != nil {
		select {
		case a.fetched <- taskID:
		default:
		}
	}

	responseTask := model.Task{
		TaskID:   taskID,
		Status:   model.TaskStatusInProgress,
		Progress: "30%",
	}
	if configured, ok := a.responseByKey[key]; ok {
		responseTask = configured
		if responseTask.TaskID == "" {
			responseTask.TaskID = taskID
		}
	}
	response := dto.TaskResponse[model.Task]{
		Code: dto.TaskSuccessCode,
		Data: responseTask,
	}
	responseBody, err := common.Marshal(response)
	if err != nil {
		return nil, err
	}
	responseBodyReader := io.ReadCloser(io.NopCloser(bytes.NewReader(responseBody)))
	if a.bodyFactory != nil {
		responseBodyReader = a.bodyFactory(ctx)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       responseBodyReader,
	}, nil
}

func (a *taskPollingFetchAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	return &relaycommon.TaskInfo{Status: model.TaskStatusInProgress}, nil
}

func (a *taskPollingFetchAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return 0
}

func (a *taskPollingFetchAdaptor) fetchCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.taskIDs)
}

func (a *taskPollingFetchAdaptor) fetchedTaskIDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.taskIDs...)
}

func seedTaskPollingChannel(t *testing.T, id int, disableSleep bool) {
	seedTaskPollingChannelWithKey(t, id, disableSleep, "sk-test")
}

func seedTaskPollingChannelWithKey(t *testing.T, id int, disableSleep bool, key string) {
	settings := dto.ChannelOtherSettings{}
	if disableSleep {
		settings.DisableTaskPollingSleep = true
	}
	seedTaskPollingChannelWithSettings(t, id, key, settings)
}

func seedTaskPollingChannelWithSettings(t *testing.T, id int, key string, settings dto.ChannelOtherSettings) {
	t.Helper()
	ch := &model.Channel{
		Id:     id,
		Type:   constant.ChannelTypeKling,
		Name:   "polling_channel",
		Key:    key,
		Status: common.ChannelStatusEnabled,
	}
	ch.SetOtherSettings(settings)
	require.NoError(t, model.DB.Create(ch).Error)
}

func TestResolveTaskPollingSchedule(t *testing.T) {
	tests := []struct {
		name            string
		settings        dto.ChannelOtherSettings
		wantConcurrency int
		wantInterval    time.Duration
	}{
		{
			name:            "new defaults",
			wantConcurrency: 4,
			wantInterval:    250 * time.Millisecond,
		},
		{
			name: "legacy skip sleep maps to zero interval",
			settings: dto.ChannelOtherSettings{
				DisableTaskPollingSleep: true,
			},
			wantConcurrency: 4,
			wantInterval:    0,
		},
		{
			name: "explicit settings override legacy flag",
			settings: dto.ChannelOtherSettings{
				DisableTaskPollingSleep: true,
				TaskPollingConcurrency:  common.GetPointer(8),
				TaskPollingIntervalMs:   common.GetPointer(125),
			},
			wantConcurrency: 8,
			wantInterval:    125 * time.Millisecond,
		},
		{
			name: "explicit zero interval",
			settings: dto.ChannelOtherSettings{
				TaskPollingIntervalMs: common.GetPointer(0),
			},
			wantConcurrency: 4,
			wantInterval:    0,
		},
		{
			name: "invalid persisted values fall back safely",
			settings: dto.ChannelOtherSettings{
				DisableTaskPollingSleep: true,
				TaskPollingConcurrency:  common.GetPointer(0),
				TaskPollingIntervalMs:   common.GetPointer(60001),
			},
			wantConcurrency: 4,
			wantInterval:    250 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			concurrency, interval := resolveTaskPollingSchedule(tt.settings)
			assert.Equal(t, tt.wantConcurrency, concurrency)
			assert.Equal(t, tt.wantInterval, interval)
		})
	}
}

func seedPollingTask(t *testing.T, channelID int, publicID string, upstreamID string) *model.Task {
	t.Helper()
	task := &model.Task{
		TaskID:    publicID,
		Platform:  constant.TaskPlatform("kling"),
		UserId:    1,
		ChannelId: channelID,
		Action:    constant.TaskActionGenerate,
		Status:    model.TaskStatusInProgress,
		Progress:  "30%",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: upstreamID,
		},
	}
	require.NoError(t, model.DB.Create(task).Error)
	return task
}

func TestUpdateVideoTasksDefaultSleepWaitsBetweenTasks(t *testing.T) {
	truncate(t)

	const channelID = 101
	seedTaskPollingChannel(t, channelID, false)
	first := seedPollingTask(t, channelID, "task_public_1", "upstream_1")
	second := seedPollingTask(t, channelID, "task_public_2", "upstream_2")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]*model.Task{
		channelID: {first, second},
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, adaptor.fetchCount())
}

func TestUpdateVideoTasksCanSkipPollingSleepPerChannel(t *testing.T) {
	truncate(t)

	const channelID = 102
	seedTaskPollingChannel(t, channelID, true)
	first := seedPollingTask(t, channelID, "task_public_3", "upstream_3")
	second := seedPollingTask(t, channelID, "task_public_4", "upstream_4")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]*model.Task{
		channelID: {first, second},
	})

	require.NoError(t, err)
	assert.Equal(t, 2, adaptor.fetchCount())
}

func TestUpdateVideoTasksHonorsPerChannelConcurrencyBound(t *testing.T) {
	truncate(t)

	const channelID = 108
	seedTaskPollingChannelWithSettings(t, channelID, "sk-test", dto.ChannelOtherSettings{
		TaskPollingConcurrency: common.GetPointer(2),
		TaskPollingIntervalMs:  common.GetPointer(0),
	})
	tasks := []*model.Task{
		seedPollingTask(t, channelID, "task_public_bound_1", "upstream_bound_1"),
		seedPollingTask(t, channelID, "task_public_bound_2", "upstream_bound_2"),
		seedPollingTask(t, channelID, "task_public_bound_3", "upstream_bound_3"),
		seedPollingTask(t, channelID, "task_public_bound_4", "upstream_bound_4"),
	}
	fetched := make(chan string, len(tasks))
	adaptor := &taskPollingFetchAdaptor{
		fetched: fetched,
		bodyFactory: func(ctx context.Context) io.ReadCloser {
			return &pollingContextReadCloser{ctx: ctx}
		},
	}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	gopool.Go(func() {
		errCh <- UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]*model.Task{
			channelID: tasks,
		})
	})

	for range 2 {
		select {
		case <-fetched:
		case <-time.After(time.Second):
			t.Fatal("configured polling workers did not start")
		}
	}
	cancel()
	require.ErrorIs(t, <-errCh, context.Canceled)
	assert.Equal(t, 2, adaptor.fetchCount())
}

func TestUpdateVideoTasksCancelsBlockedResponseBodyAndKeepsTaskPending(t *testing.T) {
	truncate(t)

	const channelID = 103
	seedTaskPollingChannel(t, channelID, true)
	task := seedPollingTask(t, channelID, "task_public_blocked_body", "upstream_blocked_body")
	adaptor := &taskPollingFetchAdaptor{
		bodyFactory: func(ctx context.Context) io.ReadCloser {
			return &pollingContextReadCloser{ctx: ctx}
		},
	}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := UpdateVideoTasks(ctx, task.Platform, map[int][]*model.Task{
		channelID: {task},
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusInProgress, persisted.Status)
	assert.Equal(t, "30%", persisted.Progress)
}

func TestUpdateVideoTasksProviderDeadlineKeepsTaskPending(t *testing.T) {
	truncate(t)

	const channelID = 104
	seedTaskPollingChannel(t, channelID, true)
	task := seedPollingTask(t, channelID, "task_public_provider_timeout", "upstream_provider_timeout")
	adaptor := &taskPollingFetchAdaptor{
		bodyFactory: func(ctx context.Context) io.ReadCloser {
			return &pollingContextReadCloser{ctx: ctx}
		},
	}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	previousTimeout := taskFetchTimeout
	taskFetchTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		GetTaskAdaptorFunc = previousFactory
		taskFetchTimeout = previousTimeout
	})

	started := time.Now()
	err := UpdateVideoTasks(context.Background(), task.Platform, map[int][]*model.Task{
		channelID: {task},
	})

	require.NoError(t, err)
	assert.Less(t, time.Since(started), time.Second)
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusInProgress, persisted.Status)
	assert.Equal(t, "30%", persisted.Progress)
}

func TestRunTaskPollingOnceAllowsSameUpstreamIDAcrossChannels(t *testing.T) {
	truncate(t)

	const firstChannelID = 105
	const secondChannelID = 106
	const sharedUpstreamID = "shared_upstream_id"
	seedTaskPollingChannelWithKey(t, firstChannelID, true, "key-first")
	seedTaskPollingChannelWithKey(t, secondChannelID, true, "key-second")
	first := seedPollingTask(t, firstChannelID, "task_public_collision_first", sharedUpstreamID)
	second := seedPollingTask(t, secondChannelID, "task_public_collision_second", sharedUpstreamID)
	adaptor := &taskPollingFetchAdaptor{
		responseByKey: map[string]model.Task{
			"key-first": {
				TaskID:   sharedUpstreamID,
				Status:   model.TaskStatusInProgress,
				Progress: "41%",
			},
			"key-second": {
				TaskID:   sharedUpstreamID,
				Status:   model.TaskStatusInProgress,
				Progress: "52%",
			},
		},
	}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	previousLimit := constant.TaskQueryLimit
	constant.TaskQueryLimit = 100
	t.Cleanup(func() {
		GetTaskAdaptorFunc = previousFactory
		constant.TaskQueryLimit = previousLimit
	})

	RunTaskPollingOnce(context.Background(), nil)

	assert.Equal(t, 2, adaptor.fetchCount())
	var persistedFirst model.Task
	var persistedSecond model.Task
	require.NoError(t, model.DB.First(&persistedFirst, first.ID).Error)
	require.NoError(t, model.DB.First(&persistedSecond, second.ID).Error)
	assert.Equal(t, "41%", persistedFirst.Progress)
	assert.Equal(t, "52%", persistedSecond.Progress)
}

func TestUpdateVideoTasksRejectsMismatchedProviderTaskID(t *testing.T) {
	truncate(t)

	const channelID = 107
	seedTaskPollingChannelWithKey(t, channelID, true, "key-mismatch")
	task := seedPollingTask(t, channelID, "task_public_mismatch", "expected_upstream_id")
	adaptor := &taskPollingFetchAdaptor{
		responseByKey: map[string]model.Task{
			"key-mismatch": {
				TaskID:   "different_upstream_id",
				Status:   model.TaskStatusSuccess,
				Progress: "100%",
			},
		},
	}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	err := UpdateVideoTasks(context.Background(), task.Platform, map[int][]*model.Task{
		channelID: {task},
	})

	require.NoError(t, err)
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusInProgress, persisted.Status)
	assert.Equal(t, "30%", persisted.Progress)
	assert.Empty(t, persisted.Data)
}

func TestUpdateVideoTasksDefaultSleepDoesNotBlockOtherChannels(t *testing.T) {
	truncate(t)

	const firstChannelID = 201
	const secondChannelID = 202
	seedTaskPollingChannel(t, firstChannelID, false)
	seedTaskPollingChannel(t, secondChannelID, false)
	firstChannelFirst := seedPollingTask(t, firstChannelID, "task_public_5", "upstream_a_1")
	firstChannelSecond := seedPollingTask(t, firstChannelID, "task_public_6", "upstream_a_2")
	secondChannelFirst := seedPollingTask(t, secondChannelID, "task_public_7", "upstream_b_1")
	secondChannelSecond := seedPollingTask(t, secondChannelID, "task_public_8", "upstream_b_2")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]*model.Task{
		firstChannelID:  {firstChannelFirst, firstChannelSecond},
		secondChannelID: {secondChannelFirst, secondChannelSecond},
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ElementsMatch(t, []string{"upstream_a_1", "upstream_b_1"}, adaptor.fetchedTaskIDs())
}

func TestUpdateVideoTasksSlowChannelDoesNotBlockOtherChannels(t *testing.T) {
	truncate(t)

	const slowChannelID = 251
	const fastChannelID = 252
	seedTaskPollingChannel(t, slowChannelID, false)
	seedTaskPollingChannel(t, fastChannelID, true)
	slowTask := seedPollingTask(t, slowChannelID, "task_public_slow", "upstream_slow_1")
	fastFirst := seedPollingTask(t, fastChannelID, "task_public_fast_1", "upstream_fast_parallel_1")
	fastSecond := seedPollingTask(t, fastChannelID, "task_public_fast_2", "upstream_fast_parallel_2")
	slowUpstreamID := slowTask.GetUpstreamTaskID()
	fastFirstUpstreamID := fastFirst.GetUpstreamTaskID()
	fastSecondUpstreamID := fastSecond.GetUpstreamTaskID()

	adaptor := &taskPollingFetchAdaptor{
		fetched:      make(chan string, 4),
		blockTaskID:  slowUpstreamID,
		blockStarted: make(chan struct{}),
		releaseBlock: make(chan struct{}),
	}
	var releaseOnce sync.Once
	releaseBlockedTask := func() {
		releaseOnce.Do(func() {
			close(adaptor.releaseBlock)
		})
	}
	t.Cleanup(releaseBlockedTask)
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	errCh := make(chan error, 1)
	gopool.Go(func() {
		errCh <- UpdateVideoTasks(context.Background(), constant.TaskPlatform("kling"), map[int][]*model.Task{
			slowChannelID: {slowTask},
			fastChannelID: {fastFirst, fastSecond},
		})
	})

	select {
	case <-adaptor.blockStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("slow channel did not start blocking")
	}

	fastFetched := make([]string, 0, 2)
	for len(fastFetched) < 2 {
		select {
		case taskID := <-adaptor.fetched:
			fastFetched = append(fastFetched, taskID)
		case <-time.After(500 * time.Millisecond):
			t.Fatal("fast channel tasks did not finish while the slow channel was blocked")
		}
	}
	assert.ElementsMatch(t, []string{fastFirstUpstreamID, fastSecondUpstreamID}, fastFetched)

	releaseBlockedTask()
	require.NoError(t, <-errCh)
	assert.ElementsMatch(t, []string{
		slowUpstreamID,
		fastFirstUpstreamID,
		fastSecondUpstreamID,
	}, adaptor.fetchedTaskIDs())
}

func TestUpdateVideoTasksMixedChannelSleepSettings(t *testing.T) {
	truncate(t)

	const sleepyChannelID = 301
	const fastChannelID = 302
	seedTaskPollingChannel(t, sleepyChannelID, false)
	seedTaskPollingChannel(t, fastChannelID, true)
	sleepyFirst := seedPollingTask(t, sleepyChannelID, "task_public_9", "upstream_sleepy_1")
	sleepySecond := seedPollingTask(t, sleepyChannelID, "task_public_10", "upstream_sleepy_2")
	fastFirst := seedPollingTask(t, fastChannelID, "task_public_11", "upstream_fast_1")
	fastSecond := seedPollingTask(t, fastChannelID, "task_public_12", "upstream_fast_2")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]*model.Task{
		sleepyChannelID: {sleepyFirst, sleepySecond},
		fastChannelID:   {fastFirst, fastSecond},
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ElementsMatch(t, []string{"upstream_sleepy_1", "upstream_fast_1", "upstream_fast_2"}, adaptor.fetchedTaskIDs())
}

func TestRunTaskPollingOnceRefundsTaskWithoutUpstreamID(t *testing.T) {
	truncate(t)

	const userID, quota = 51, 1800
	seedUser(t, userID, 7000)
	task := &model.Task{
		Platform:      constant.TaskPlatform("kling"),
		UserId:        userID,
		Quota:         quota,
		Status:        model.TaskStatusInProgress,
		Progress:      "30%",
		BillingStatus: model.TaskBillingStatusReserved,
		CreatedAt:     time.Now().Unix(),
		UpdatedAt:     time.Now().Unix(),
		PrivateData: model.TaskPrivateData{
			BillingSource: BillingSourceWallet,
		},
	}
	require.NoError(t, model.DB.Create(task).Error)

	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return &taskPollingFetchAdaptor{} }
	previousLimit := constant.TaskQueryLimit
	constant.TaskQueryLimit = 100
	t.Cleanup(func() {
		GetTaskAdaptorFunc = previousFactory
		constant.TaskQueryLimit = previousLimit
	})

	summary := RunTaskPollingOnce(context.Background(), nil)
	assert.Equal(t, 1, summary.NullTasksFailed)
	assert.Equal(t, 7000+quota, getUserQuota(t, userID))

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, persisted.Status)
	assert.Equal(t, model.TaskBillingStatusRefunded, persisted.BillingStatus)
	assert.Equal(t, quota, persisted.RefundedQuota)
}

func TestUpdateVideoTasksRefundsWhenChannelWasDeleted(t *testing.T) {
	truncate(t)

	const userID, channelID, quota = 52, 9090, 2100
	seedUser(t, userID, 8000)
	task := &model.Task{
		TaskID:        "task_deleted_channel",
		Platform:      constant.TaskPlatform("kling"),
		UserId:        userID,
		ChannelId:     channelID,
		Quota:         quota,
		Status:        model.TaskStatusInProgress,
		Progress:      "30%",
		BillingStatus: model.TaskBillingStatusReserved,
		CreatedAt:     time.Now().Unix(),
		UpdatedAt:     time.Now().Unix(),
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "upstream_deleted_channel",
			BillingSource:  BillingSourceWallet,
		},
	}
	require.NoError(t, model.DB.Create(task).Error)

	err := UpdateVideoTasks(context.Background(), task.Platform, map[int][]*model.Task{
		channelID: {task},
	})
	require.NoError(t, err)
	assert.Equal(t, 8000+quota, getUserQuota(t, userID))

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, persisted.Status)
	assert.Equal(t, model.TaskBillingStatusRefunded, persisted.BillingStatus)
	assert.Equal(t, quota, persisted.RefundedQuota)
}

func TestSweepTimedOutTasksRefundsLegacyActiveButDoesNotReplayLegacyTerminal(t *testing.T) {
	truncate(t)

	const userID, activeQuota, terminalQuota = 53, 1600, 900
	seedUser(t, userID, 5000)
	active := &model.Task{
		TaskID:     "legacy_active_timeout",
		Platform:   constant.TaskPlatform("kling"),
		UserId:     userID,
		Quota:      activeQuota,
		Status:     model.TaskStatusInProgress,
		Progress:   "50%",
		SubmitTime: time.Now().Add(-2 * time.Hour).Unix(),
		PrivateData: model.TaskPrivateData{
			BillingSource: BillingSourceWallet,
		},
	}
	require.NoError(t, model.DB.Create(active).Error)
	terminal := &model.Task{
		TaskID:     "legacy_terminal_timeout",
		Platform:   constant.TaskPlatform("kling"),
		UserId:     userID,
		Quota:      terminalQuota,
		Status:     model.TaskStatusFailure,
		Progress:   "100%",
		SubmitTime: time.Now().Add(-3 * time.Hour).Unix(),
		PrivateData: model.TaskPrivateData{
			BillingSource: BillingSourceWallet,
		},
	}
	require.NoError(t, model.DB.Create(terminal).Error)

	previousTimeout := constant.TaskTimeoutMinutes
	constant.TaskTimeoutMinutes = 60
	t.Cleanup(func() { constant.TaskTimeoutMinutes = previousTimeout })
	sweepTimedOutTasks(context.Background())

	assert.Equal(t, 5000+activeQuota, getUserQuota(t, userID))
	var persistedActive model.Task
	require.NoError(t, model.DB.First(&persistedActive, active.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, persistedActive.Status)
	assert.Equal(t, model.TaskBillingStatusRefunded, persistedActive.BillingStatus)
	var persistedTerminal model.Task
	require.NoError(t, model.DB.First(&persistedTerminal, terminal.ID).Error)
	assert.Empty(t, persistedTerminal.BillingStatus)
	assert.Zero(t, persistedTerminal.RefundedQuota)
}
