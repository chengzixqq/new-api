/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Banner,
  Button,
  Card,
  Col,
  Collapse,
  Empty,
  Form,
  InputNumber,
  Modal,
  Popconfirm,
  Radio,
  RadioGroup,
  Row,
  Select,
  Space,
  Spin,
  Switch,
  Table,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import {
  Activity,
  Edit,
  KeyRound,
  Play,
  Plus,
  Save,
  ShieldCheck,
  Trash2,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess, toBoolean } from '../../../helpers';

const { Text, Title } = Typography;

const SETTING_DEFAULTS = {
  'model_health_setting.enabled': false,
  'model_health_setting.pool_details_enabled': false,
  'model_health_setting.multi_sample_enabled': false,
  'model_health_setting.default_interval_seconds': 300,
  'model_health_setting.default_timeout_seconds': 45,
  'model_health_setting.default_sampling_mode': 'confirm_on_failure',
  'model_health_setting.default_samples_per_run': 3,
  'model_health_setting.default_minimum_successes': 2,
  'model_health_setting.default_sample_spacing_seconds': 3,
  'model_health_setting.concurrency': 4,
  'model_health_setting.retention_days': 30,
  'model_health_setting.healthy_threshold': 99,
  'model_health_setting.fluctuating_threshold': 95,
  'model_health_setting.passive_min_samples': 30,
  'model_health_setting.active_min_samples': 3,
  'perf_metrics_setting.enabled': true,
  'perf_metrics_setting.flush_interval': 5,
  'perf_metrics_setting.bucket_time': '5min',
  'perf_metrics_setting.retention_days': 30,
};

const TARGET_DEFAULTS = {
  name: '',
  mode: 'local',
  token_id: undefined,
  protocol: 'openai_chat',
  endpoint: '',
  api_key: '',
  observed_groups: [],
  observed_channel_ids: [],
  public_group_alias: '',
  public_pool_alias: '',
  publish_pool_detail: false,
  public: false,
  enabled: true,
  interval_seconds: 300,
  timeout_seconds: 45,
  latency_slo_ms: 0,
  sampling_mode: 'confirm_on_failure',
  samples_per_run: 3,
  minimum_successes: 2,
  sample_spacing_seconds: 3,
  models: [],
};

const EMPTY_ADMIN_OPTIONS = {
  tokens: [],
  groups: [],
  models: [],
  channels: [],
  groupModels: {},
  hasModelRelations: false,
};

const PROTOCOLS = [
  { value: 'openai_chat', label: 'OpenAI Chat Completions' },
  { value: 'openai_responses', label: 'OpenAI Responses' },
  { value: 'anthropic_messages', label: 'Anthropic Messages' },
  { value: 'gemini_generate_content', label: 'Gemini GenerateContent' },
];

function unwrapResponse(response) {
  return response?.data?.data ?? response?.data ?? {};
}

function optionNumber(options, key) {
  const value = Number(options?.[key]);
  return Number.isFinite(value) ? value : SETTING_DEFAULTS[key];
}

function boundedInteger(value, fallback, min, max) {
  const number = Number(value);
  if (!Number.isFinite(number)) return fallback;
  return Math.min(max, Math.max(min, Math.trunc(number)));
}

function samplingMode(value, fallback = 'fixed') {
  return value === 'confirm_on_failure' || value === 'fixed' ? value : fallback;
}

function samplingBudgetExceeded(config) {
  const samples = boundedInteger(config.samples_per_run, 1, 1, 5);
  const timeout = boundedInteger(config.timeout_seconds, 45, 1, 300);
  const spacing = boundedInteger(config.sample_spacing_seconds, 3, 1, 30);
  const interval = boundedInteger(config.interval_seconds, 300, 60, 3600);
  return samples * timeout + Math.max(0, samples - 1) * spacing > interval;
}

function normalizeValidationResult(raw) {
  const source = raw?.batch ?? raw?.result ?? raw ?? {};
  const attempts = (Array.isArray(source.attempts) ? source.attempts : []).map(
    (attempt, index) => ({
      index: boundedInteger(attempt?.attempt_index, index + 1, 1, 5),
      status: String(attempt?.status ?? ''),
      latency: Number(attempt?.latency_ms),
      httpStatus: Number(attempt?.http_status),
      errorClass: String(attempt?.error_class ?? ''),
    }),
  );
  const plannedAttempts = boundedInteger(
    source.planned_attempts,
    attempts.length || 1,
    1,
    5,
  );
  const actualAttempts = boundedInteger(
    source.actual_attempts,
    attempts.length,
    0,
    5,
  );
  const successCount = boundedInteger(
    source.success_count,
    attempts.filter((attempt) =>
      ['healthy', 'ok', 'success'].includes(attempt.status.toLowerCase()),
    ).length,
    0,
    actualAttempts,
  );
  const availability = Number(source.availability);
  return {
    status: String(source.batch_status ?? source.status ?? ''),
    availability: Number.isFinite(availability)
      ? availability
      : actualAttempts > 0
        ? (successCount / actualAttempts) * 100
        : null,
    plannedAttempts,
    actualAttempts,
    successCount,
    attempts,
  };
}

function validationStatusMeta(status) {
  const value = String(status || '').toLowerCase();
  if (['healthy', 'normal', 'ok', 'success'].includes(value)) {
    return { color: 'green', label: 'Healthy' };
  }
  if (['fluctuating', 'degraded', 'warning'].includes(value)) {
    return { color: 'orange', label: 'Fluctuating' };
  }
  if (['idle', 'pending'].includes(value)) {
    return { color: 'grey', label: 'Idle' };
  }
  return { color: 'red', label: 'Unstable' };
}

function uniqueNames(values, limit = 20) {
  return [
    ...new Set(
      (Array.isArray(values) ? values : [])
        .map((value) => String(value ?? '').trim())
        .filter(Boolean),
    ),
  ].slice(0, limit);
}

function normalizeNamedOption(item, fallbackKey) {
  if (typeof item === 'string') {
    const name = item.trim();
    return name ? { name, label: name } : null;
  }
  const name = String(
    item?.name ?? item?.value ?? item?.[fallbackKey] ?? '',
  ).trim();
  if (!name) return null;
  const label = String(
    item?.display_name ?? item?.label ?? item?.alias ?? '',
  ).trim();
  return {
    name,
    label: label || name,
    groups: uniqueNames(item?.groups ?? item?.group_names ?? [], 1000),
    models: uniqueNames(item?.models ?? item?.model_names ?? [], 10000),
  };
}

function normalizeTokenOption(item) {
  const id = Number(item?.id ?? item?.token_id);
  if (!Number.isInteger(id) || id <= 0) return null;
  const name = String(item?.name ?? item?.token_name ?? `#${id}`).trim();
  const explicitAvailable = item?.available ?? item?.enabled;
  const available =
    explicitAvailable !== undefined
      ? Boolean(explicitAvailable)
      : item?.status === undefined ||
        item.status === 1 ||
        item.status === 'enabled';
  return {
    id,
    name,
    group: String(item?.group ?? item?.group_name ?? '').trim(),
    quota:
      item?.remaining_quota ?? item?.remain_quota ?? item?.quota ?? undefined,
    unlimited: item?.unlimited_quota === true || item?.unlimited === true,
    expiresAt:
      item?.expired_time ?? item?.expires_at ?? item?.expired_at ?? undefined,
    status: item?.status,
    available,
    marked: item?.marked ?? item?.is_probe_token ?? item?.probe_enabled ?? true,
    referenceCount: Number(
      item?.reference_count ?? item?.target_count ?? item?.references ?? 0,
    ),
    modelLimitsEnabled: Boolean(
      item?.model_limits_enabled ?? item?.models_enabled,
    ),
    modelLimits: uniqueNames(item?.model_limits ?? [], 10000),
  };
}

function normalizeChannelOption(item) {
  const id = Number(item?.id ?? item?.channel_id);
  if (!Number.isInteger(id) || id <= 0) return null;
  const status = Number(item?.status ?? 0);
  const explicitAvailable = item?.available ?? item?.enabled;
  return {
    id,
    name: String(item?.name ?? item?.channel_name ?? `#${id}`).trim(),
    status,
    available:
      explicitAvailable !== undefined
        ? Boolean(explicitAvailable)
        : status === 1,
    groups: uniqueNames(item?.groups ?? item?.group_names ?? [], 1000),
    models: uniqueNames(item?.models ?? item?.model_names ?? [], 10000),
  };
}

function normalizeAdminOptions(raw) {
  const source = raw?.options ?? raw ?? {};
  const tokenItems = source.tokens ?? source.probe_tokens ?? [];
  const groupItems = source.groups ?? source.group_options ?? [];
  const modelItems = source.models ?? source.model_options ?? [];
  const channelItems = source.channels ?? source.channel_options ?? [];
  const tokens = (Array.isArray(tokenItems) ? tokenItems : [])
    .map(normalizeTokenOption)
    .filter(Boolean);
  const groups = (Array.isArray(groupItems) ? groupItems : [])
    .map((item) => normalizeNamedOption(item, 'group'))
    .filter(Boolean);
  const models = (Array.isArray(modelItems) ? modelItems : [])
    .map((item) => normalizeNamedOption(item, 'model'))
    .filter(Boolean);
  const channels = (Array.isArray(channelItems) ? channelItems : [])
    .map(normalizeChannelOption)
    .filter(Boolean);
  const groupModels = {};
  let hasModelRelations = false;

  const relationSource = source.group_models ?? source.groupModels;
  if (relationSource && typeof relationSource === 'object') {
    hasModelRelations = true;
    for (const [group, names] of Object.entries(relationSource)) {
      groupModels[group] = uniqueNames(names, 10000);
    }
  }
  for (const group of groups) {
    if (group.models.length > 0) {
      hasModelRelations = true;
      groupModels[group.name] = uniqueNames(
        [...(groupModels[group.name] || []), ...group.models],
        10000,
      );
    }
  }
  for (const model of models) {
    if (model.groups.length > 0) hasModelRelations = true;
    for (const group of model.groups) {
      groupModels[group] = uniqueNames(
        [...(groupModels[group] || []), model.name],
        10000,
      );
    }
  }

  return {
    tokens: tokens.sort((left, right) => left.name.localeCompare(right.name)),
    groups: groups.sort((left, right) => left.label.localeCompare(right.label)),
    models: models.sort((left, right) => left.label.localeCompare(right.label)),
    channels: channels.sort((left, right) =>
      left.name.localeCompare(right.name),
    ),
    groupModels,
    hasModelRelations,
  };
}

function normalizeProbeTokens(raw) {
  const source = Array.isArray(raw)
    ? raw
    : (raw?.tokens ?? raw?.probe_tokens ?? []);
  return (Array.isArray(source) ? source : [])
    .map(normalizeTokenOption)
    .filter(Boolean)
    .sort((left, right) => left.name.localeCompare(right.name));
}

function normalizeTargetModels(models) {
  const seen = new Set();
  const result = [];
  for (const item of Array.isArray(models) ? models : []) {
    const name = String(item?.name ?? item ?? '').trim();
    if (!name || seen.has(name) || result.length >= 20) continue;
    seen.add(name);
    result.push({
      name,
      public_alias: String(item?.public_alias ?? item?.alias ?? name).trim(),
      required: item?.required !== false,
    });
  }
  return result;
}

function normalizeTarget(target, defaults) {
  const observedGroups = uniqueNames(
    Array.isArray(target?.observed_groups)
      ? target.observed_groups
      : target?.observed_group
        ? [target.observed_group]
        : [],
  );
  const isExistingTarget = Boolean(target);
  const normalizedSamples = boundedInteger(
    target?.samples_per_run,
    isExistingTarget ? 1 : defaults.samplesPerRun,
    1,
    5,
  );
  const normalizedMinimumSuccesses = boundedInteger(
    target?.minimum_successes,
    isExistingTarget ? 1 : defaults.minimumSuccesses,
    1,
    normalizedSamples,
  );
  return {
    ...TARGET_DEFAULTS,
    ...target,
    endpoint: '',
    api_key: '',
    interval_seconds:
      Number(target?.interval_seconds) || defaults.intervalSeconds,
    timeout_seconds: Number(target?.timeout_seconds) || defaults.timeoutSeconds,
    latency_slo_ms: Number(target?.latency_slo_ms || 0),
    sampling_mode: samplingMode(
      target?.sampling_mode,
      isExistingTarget ? 'fixed' : defaults.samplingMode,
    ),
    samples_per_run: normalizedSamples,
    minimum_successes: normalizedSamples === 1 ? 1 : normalizedMinimumSuccesses,
    sample_spacing_seconds: boundedInteger(
      target?.sample_spacing_seconds,
      isExistingTarget ? 3 : defaults.sampleSpacingSeconds,
      1,
      30,
    ),
    observed_groups: observedGroups,
    observed_channel_ids: [
      ...new Set(
        (Array.isArray(target?.observed_channel_ids)
          ? target.observed_channel_ids
          : []
        )
          .map(Number)
          .filter((id) => Number.isInteger(id) && id > 0),
      ),
    ].slice(0, 20),
    public_pool_alias: String(target?.public_pool_alias ?? '').trim(),
    publish_pool_detail: Boolean(target?.publish_pool_detail),
    models: normalizeTargetModels(target?.models),
  };
}

function selectableChannelOptions(
  adminOptions,
  selectedGroups,
  selectedModels,
  selectedChannelIds,
) {
  const selected = new Set(selectedChannelIds.map(Number));
  const known = new Set(adminOptions.channels.map((channel) => channel.id));
  const options = adminOptions.channels
    .filter((channel) => {
      if (selected.has(channel.id)) return true;
      if (!channel.available) return false;
      if (
        selectedGroups.length > 0 &&
        !selectedGroups.some((group) => channel.groups.includes(group))
      ) {
        return false;
      }
      if (
        selectedModels.length > 0 &&
        !selectedModels.every((model) => channel.models.includes(model))
      ) {
        return false;
      }
      return true;
    })
    .map((channel) => ({
      value: channel.id,
      label: `#${channel.id} · ${channel.name}`,
      disabled: !channel.available,
      unavailable: !channel.available,
    }));

  for (const id of selected) {
    if (known.has(id)) continue;
    options.push({
      value: id,
      label: `#${id}`,
      disabled: true,
      unavailable: true,
    });
  }
  return options;
}

function selectedModelOptions(
  adminOptions,
  selectedGroups,
  selectedModels,
  mode,
  tokenId,
) {
  const selected = new Set(selectedModels);
  let names = adminOptions.models.map((item) => item.name);
  if (selectedGroups.length > 0 && adminOptions.hasModelRelations) {
    const sets = selectedGroups.map(
      (group) => new Set(adminOptions.groupModels[group] || []),
    );
    names = names.filter((name) => sets.every((set) => set.has(name)));
  }
  const token = adminOptions.tokens.find((item) => item.id === Number(tokenId));
  if (mode === 'local' && token?.modelLimitsEnabled) {
    const allowed = new Set(token.modelLimits);
    names = names.filter((name) => allowed.has(name));
  }
  const labels = new Map(
    adminOptions.models.map((item) => [item.name, item.label]),
  );
  return uniqueNames([...names, ...selectedModels], 10000)
    .sort((left, right) => left.localeCompare(right))
    .map((name) => ({
      value: name,
      label: labels.get(name) || name,
      unknown: !labels.has(name) && selected.has(name),
    }));
}

function automaticGroupAlias(selectedGroups, models) {
  if (selectedGroups.length > 0) {
    return selectedGroups.join(' / ');
  }
  return models[0]?.public_alias || models[0]?.name || '';
}

function automaticTargetName(draft, adminOptions) {
  const source =
    draft.mode === 'local'
      ? adminOptions.tokens.find((token) => token.id === Number(draft.token_id))
          ?.name || 'local'
      : 'upstream';
  const firstModel = draft.models[0]?.name;
  const suffix = draft.models.length > 1 ? ` +${draft.models.length - 1}` : '';
  return [source, firstModel ? `${firstModel}${suffix}` : '']
    .filter(Boolean)
    .join(' · ');
}

function tokenOptionLabel(token, locale, t) {
  return [
    `#${token.id}`,
    token.name,
    token.group,
    `${t('Remaining quota')}: ${formatQuota(token, locale)}`,
    `${t('Expires')}: ${formatExpiry(token, locale, t('Never'))}`,
  ]
    .filter(Boolean)
    .join(' · ');
}

function formatQuota(token, locale) {
  if (token.unlimited) return '∞';
  const value = Number(token.quota);
  return Number.isFinite(value) ? value.toLocaleString(locale) : '--';
}

function formatExpiry(token, locale, neverText) {
  const raw = Number(token.expiresAt);
  if (!Number.isFinite(raw) || raw <= 0) return neverText;
  const timestamp = raw > 100000000000 ? raw : raw * 1000;
  return new Date(timestamp).toLocaleDateString(locale);
}

function ProbeTokenManagerModal({
  visible,
  loading,
  tokens,
  updatingId,
  onToggle,
  onCancel,
  t,
  locale,
}) {
  const columns = useMemo(
    () => [
      {
        title: t('Token'),
        dataIndex: 'name',
        render: (name, token) => (
          <div>
            <div className='font-medium'>{name}</div>
            <Text type='tertiary' size='small'>
              #{token.id}
            </Text>
          </div>
        ),
      },
      {
        title: t('Group'),
        dataIndex: 'group',
        render: (group) => group || '--',
      },
      {
        title: t('Remaining quota'),
        render: (_, token) => formatQuota(token, locale),
      },
      {
        title: t('Expires'),
        render: (_, token) => formatExpiry(token, locale, t('Never')),
      },
      {
        title: t('Status'),
        render: (_, token) => (
          <Tag color={token.available ? 'green' : 'grey'}>
            {token.available ? t('Enabled') : t('Disabled')}
          </Tag>
        ),
      },
      {
        title: t('Probe token'),
        width: 170,
        render: (_, token) => {
          const referenced = token.marked && token.referenceCount > 0;
          return (
            <div>
              <Switch
                checked={Boolean(token.marked)}
                loading={updatingId === token.id}
                disabled={referenced || (!token.available && !token.marked)}
                onChange={(marked) => onToggle(token, marked)}
              />
              {referenced && (
                <div className='mt-1 text-xs text-semi-color-text-2'>
                  {t('Used by {{count}} targets', {
                    count: token.referenceCount,
                  })}
                </div>
              )}
            </div>
          );
        },
      },
    ],
    [locale, onToggle, t, updatingId],
  );

  return (
    <Modal
      title={t('Manage probe tokens')}
      visible={visible}
      width={820}
      onCancel={onCancel}
      footer={
        <Button type='primary' onClick={onCancel}>
          {t('Done')}
        </Button>
      }
    >
      <Banner
        type='info'
        className='mb-4'
        description={t(
          'Only tokens marked here can be selected by local health targets.',
        )}
      />
      <Table
        columns={columns}
        dataSource={tokens}
        rowKey='id'
        loading={loading}
        pagination={false}
        scroll={{ x: 720 }}
        empty={<Empty description={t('No tokens available')} />}
      />
    </Modal>
  );
}

function ModelPublicationRows({ models, onChange, t }) {
  if (models.length === 0) return null;
  return (
    <div className='space-y-2'>
      {models.map((model) => (
        <div
          key={model.name}
          className='grid grid-cols-1 gap-3 rounded-lg border border-semi-color-border bg-semi-color-fill-0 p-3 md:grid-cols-[minmax(150px,1fr)_minmax(190px,1.2fr)_140px] md:items-end'
        >
          <div className='min-w-0'>
            <Text type='tertiary' size='small'>
              {t('Internal model')}
            </Text>
            <div className='truncate font-medium' title={model.name}>
              {model.name}
            </div>
          </div>
          <Form.Input
            label={t('Public model alias')}
            value={model.public_alias}
            onChange={(value) =>
              onChange(
                models.map((item) =>
                  item.name === model.name
                    ? { ...item, public_alias: value }
                    : item,
                ),
              )
            }
          />
          <label className='flex h-8 items-center gap-2'>
            <Switch
              checked={model.required}
              onChange={(required) =>
                onChange(
                  models.map((item) =>
                    item.name === model.name ? { ...item, required } : item,
                  ),
                )
              }
            />
            <span>{t('Required model')}</span>
          </label>
        </div>
      ))}
    </div>
  );
}

function ValidationResultPanel({ result, t }) {
  if (!result) return null;
  const batchMeta = validationStatusMeta(result.status);
  return (
    <div className='mt-4 rounded-lg border border-semi-color-border bg-semi-color-fill-0 p-4'>
      <div className='mb-3 flex flex-wrap items-center justify-between gap-2'>
        <div className='font-semibold'>{t('Probe validation result')}</div>
        <Tag color={batchMeta.color}>{t(batchMeta.label)}</Tag>
      </div>
      <div className='grid grid-cols-2 gap-3 text-sm sm:grid-cols-4'>
        <div>
          <Text type='tertiary' size='small'>
            {t('Batch availability')}
          </Text>
          <div className='mt-1 font-mono font-semibold tabular-nums'>
            {Number.isFinite(result.availability)
              ? `${result.availability.toFixed(2)}%`
              : '--'}
          </div>
        </div>
        <div>
          <Text type='tertiary' size='small'>
            {t('Planned attempts')}
          </Text>
          <div className='mt-1 font-mono font-semibold tabular-nums'>
            {result.plannedAttempts}
          </div>
        </div>
        <div>
          <Text type='tertiary' size='small'>
            {t('Actual attempts')}
          </Text>
          <div className='mt-1 font-mono font-semibold tabular-nums'>
            {result.actualAttempts}
          </div>
        </div>
        <div>
          <Text type='tertiary' size='small'>
            {t('Successful attempts')}
          </Text>
          <div className='mt-1 font-mono font-semibold tabular-nums'>
            {result.successCount}
          </div>
        </div>
      </div>
      {result.attempts.length > 0 && (
        <div className='mt-3 space-y-2'>
          {result.attempts.map((attempt) => {
            const attemptMeta = validationStatusMeta(attempt.status);
            return (
              <div
                key={attempt.index}
                className='grid grid-cols-2 gap-2 rounded-md border border-semi-color-border bg-semi-color-bg-0 px-3 py-2 text-xs sm:grid-cols-[minmax(100px,1fr)_repeat(3,minmax(90px,1fr))] sm:items-center'
              >
                <div className='flex items-center gap-2'>
                  <span className='font-medium'>
                    {t('Attempt {{index}}', { index: attempt.index })}
                  </span>
                  <Tag size='small' color={attemptMeta.color}>
                    {t(attemptMeta.label)}
                  </Tag>
                </div>
                <div>
                  <Text type='tertiary' size='small'>
                    {t('Latest latency')}
                  </Text>
                  <div>
                    {Number.isFinite(attempt.latency) && attempt.latency > 0
                      ? `${Math.round(attempt.latency)} ms`
                      : '--'}
                  </div>
                </div>
                <div>
                  <Text type='tertiary' size='small'>
                    {t('HTTP status')}
                  </Text>
                  <div>
                    {Number.isFinite(attempt.httpStatus) &&
                    attempt.httpStatus > 0
                      ? attempt.httpStatus
                      : '--'}
                  </div>
                </div>
                <div className='min-w-0'>
                  <Text type='tertiary' size='small'>
                    {t('Error class')}
                  </Text>
                  <div className='truncate' title={attempt.errorClass}>
                    {attempt.errorClass || '--'}
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function TargetModal({
  visible,
  target,
  defaults,
  adminOptions,
  optionsLoading,
  saving,
  onCancel,
  onSave,
  onValidate,
  onManageTokens,
  t,
  locale,
  multiSampleEnabled,
}) {
  const [draft, setDraft] = useState(() => normalizeTarget(null, defaults));
  const [nameTouched, setNameTouched] = useState(false);
  const [groupAliasTouched, setGroupAliasTouched] = useState(false);
  const [validationResult, setValidationResult] = useState(null);

  useEffect(() => {
    setDraft(normalizeTarget(target, defaults));
    setNameTouched(Boolean(target?.name));
    setGroupAliasTouched(Boolean(target?.public_group_alias));
    setValidationResult(null);
  }, [defaults, target, visible]);

  const selectedModelNames = useMemo(
    () => draft.models.map((model) => model.name),
    [draft.models],
  );
  const modelOptions = useMemo(
    () =>
      selectedModelOptions(
        adminOptions,
        draft.observed_groups,
        selectedModelNames,
        draft.mode,
        draft.token_id,
      ),
    [
      adminOptions,
      draft.mode,
      draft.observed_groups,
      draft.token_id,
      selectedModelNames,
    ],
  );
  const unknownModels = useMemo(
    () =>
      modelOptions.filter((option) => option.unknown).map((item) => item.value),
    [modelOptions],
  );
  const channelOptions = useMemo(
    () =>
      selectableChannelOptions(
        adminOptions,
        draft.observed_groups,
        selectedModelNames,
        draft.observed_channel_ids,
      ),
    [
      adminOptions,
      draft.observed_channel_ids,
      draft.observed_groups,
      selectedModelNames,
    ],
  );
  const unavailableChannelIds = useMemo(
    () =>
      channelOptions
        .filter(
          (option) =>
            option.unavailable &&
            draft.observed_channel_ids.includes(Number(option.value)),
        )
        .map((option) => Number(option.value)),
    [channelOptions, draft.observed_channel_ids],
  );
  const samplingEstimate = useMemo(() => {
    const interval = boundedInteger(draft.interval_seconds, 300, 60, 3600);
    const samples = boundedInteger(draft.samples_per_run, 1, 1, 5);
    const modelCount = draft.models.length;
    const cyclesPerDay = Math.ceil(86400 / interval);
    return {
      maxAttemptsPerDay: cyclesPerDay * samples * modelCount,
      modelCount,
      budgetExceeded: samplingBudgetExceeded(draft),
    };
  }, [draft]);

  const updateDraft = (changes, updateGenerated = true) => {
    setValidationResult(null);
    setDraft((current) => {
      const next = { ...current, ...changes };
      if (updateGenerated && !groupAliasTouched) {
        next.public_group_alias = automaticGroupAlias(
          next.observed_groups,
          next.models,
        );
      }
      if (updateGenerated && !nameTouched) {
        next.name = automaticTargetName(next, adminOptions);
      }
      return next;
    });
  };

  const setSelectedModels = (values) => {
    const names = uniqueNames(values);
    if ((Array.isArray(values) ? values : []).length > 20) {
      showError(t('A target supports at most 20 models'));
    }
    const existing = new Map(draft.models.map((model) => [model.name, model]));
    updateDraft({
      models: names.map(
        (name) =>
          existing.get(name) || {
            name,
            public_alias: name,
            required: true,
          },
      ),
    });
  };

  const setObservedGroups = (values) => {
    updateDraft({ observed_groups: uniqueNames(values) });
  };

  const buildPayload = () => {
    const models = normalizeTargetModels(draft.models);
    if (models.length === 0) throw new Error('Add at least one model');
    if (models.length > 20)
      throw new Error('A target supports at most 20 models');
    if (models.some((model) => !model.public_alias)) {
      throw new Error('Public model alias is required');
    }
    const observedGroups = uniqueNames(draft.observed_groups);
    const observedChannelIds = [
      ...new Set(draft.observed_channel_ids.map(Number)),
    ].filter((id) => Number.isInteger(id) && id > 0);
    if (observedChannelIds.length > 20) {
      throw new Error('A target supports at most 20 channels');
    }
    const publicGroupAlias = String(draft.public_group_alias || '').trim();
    if ((draft.public || draft.publish_pool_detail) && !publicGroupAlias) {
      throw new Error('Public group alias is required');
    }
    const publicPoolAlias = String(draft.public_pool_alias || '').trim();
    if (draft.publish_pool_detail) {
      if (!publicPoolAlias) throw new Error('Public pool name is required');
      const availableChannelSelected = observedChannelIds.some((id) =>
        channelOptions.some(
          (option) => Number(option.value) === id && !option.unavailable,
        ),
      );
      const originalChannelIds = [
        ...new Set(
          (Array.isArray(target?.observed_channel_ids)
            ? target.observed_channel_ids
            : []
          )
            .map(Number)
            .filter((id) => Number.isInteger(id) && id > 0),
        ),
      ].sort((left, right) => left - right);
      const selectedChannelIds = [...observedChannelIds].sort(
        (left, right) => left - right,
      );
      const staleBindingUnchanged =
        target?.publish_pool_detail &&
        originalChannelIds.length === selectedChannelIds.length &&
        originalChannelIds.every(
          (id, index) => id === selectedChannelIds[index],
        );
      if (
        observedChannelIds.length === 0 ||
        (!availableChannelSelected && !staleBindingUnchanged)
      ) {
        throw new Error(
          'A published pool needs a public pool name and at least one available channel.',
        );
      }
    }
    const samplesPerRun = boundedInteger(draft.samples_per_run, 1, 1, 5);
    const minimumSuccesses =
      samplesPerRun === 1
        ? 1
        : boundedInteger(draft.minimum_successes, 1, 1, samplesPerRun);
    const sampleSpacingSeconds = boundedInteger(
      draft.sample_spacing_seconds,
      3,
      1,
      30,
    );
    if (Number(draft.minimum_successes) > samplesPerRun) {
      throw new Error('Minimum successes must not exceed samples per run.');
    }
    if (
      samplingBudgetExceeded({
        ...draft,
        samples_per_run: samplesPerRun,
        minimum_successes: minimumSuccesses,
        sample_spacing_seconds: sampleSpacingSeconds,
      })
    ) {
      throw new Error('The sampling time budget exceeds the target interval.');
    }
    const payload = {
      name:
        String(draft.name || '').trim() ||
        automaticTargetName({ ...draft, models }, adminOptions),
      mode: draft.mode,
      protocol: draft.protocol,
      observed_groups: observedGroups,
      observed_group: observedGroups[0] || '',
      observed_channel_ids: observedChannelIds,
      public_group_alias: publicGroupAlias,
      public_pool_alias: publicPoolAlias,
      publish_pool_detail: Boolean(draft.publish_pool_detail),
      public: Boolean(draft.public),
      enabled: Boolean(draft.enabled),
      interval_seconds: Number(draft.interval_seconds),
      timeout_seconds: Number(draft.timeout_seconds),
      latency_slo_ms: Number(draft.latency_slo_ms || 0),
      sampling_mode: samplingMode(draft.sampling_mode),
      samples_per_run: samplesPerRun,
      minimum_successes: minimumSuccesses,
      sample_spacing_seconds: sampleSpacingSeconds,
      models,
    };
    if (!payload.name) throw new Error('Target name is required');
    if (draft.mode === 'local') {
      payload.token_id = Number(draft.token_id);
      if (!Number.isInteger(payload.token_id) || payload.token_id <= 0) {
        throw new Error('Select a probe token');
      }
    } else {
      payload.endpoint = String(draft.endpoint || '').trim();
      payload.api_key = String(draft.api_key || '').trim();
      if (!target && (!payload.endpoint || !payload.api_key)) {
        throw new Error('Endpoint and API key are required');
      }
    }
    if (target?.id) payload.id = target.id;
    return payload;
  };

  const submit = async (handler, validation = false) => {
    try {
      const result = await handler(buildPayload());
      if (validation && result) {
        setValidationResult(normalizeValidationResult(result));
      }
    } catch (error) {
      showError(t(error.message));
    }
  };

  const tokenOptions = useMemo(() => {
    const options = adminOptions.tokens.map((token) => ({
      value: token.id,
      label: tokenOptionLabel(token, locale, t),
    }));
    if (
      target?.token_id &&
      !options.some((option) => option.value === Number(target.token_id))
    ) {
      options.push({
        value: Number(target.token_id),
        label: `#${target.token_id}`,
      });
    }
    return options;
  }, [adminOptions.tokens, locale, t, target?.token_id]);

  return (
    <Modal
      title={target ? t('Edit') : t('Add target')}
      visible={visible}
      onCancel={onCancel}
      width={880}
      footer={
        <div className='flex justify-end gap-2'>
          <Button onClick={onCancel}>{t('Cancel')}</Button>
          <Button
            icon={<ShieldCheck size={14} />}
            onClick={() => void submit(onValidate, true)}
            loading={saving}
          >
            {t('Validate')}
          </Button>
          <Button
            type='primary'
            icon={<Save size={14} />}
            onClick={() => void submit(onSave)}
            loading={saving}
          >
            {t('Save')}
          </Button>
        </div>
      }
    >
      <Spin spinning={optionsLoading}>
        <Form layout='vertical'>
          <div className='mb-3 text-sm font-semibold'>{t('Probe source')}</div>
          <Row gutter={[16, 16]}>
            <Col xs={24} md={12}>
              <Form.Select
                label={t('Probe mode')}
                value={draft.mode}
                optionList={[
                  { value: 'local', label: t('Local token') },
                  { value: 'upstream', label: t('Upstream HTTPS endpoint') },
                ]}
                onChange={(mode) => updateDraft({ mode })}
              />
            </Col>
            <Col xs={24} md={12}>
              <Form.Select
                label={t('Protocol')}
                value={draft.protocol}
                optionList={PROTOCOLS}
                onChange={(protocol) => updateDraft({ protocol }, false)}
              />
            </Col>
          </Row>

          {draft.mode === 'local' ? (
            <div>
              <Row gutter={[12, 12]} align='bottom'>
                <Col xs={24} md={18}>
                  <Form.Select
                    label={t('Probe token')}
                    value={draft.token_id}
                    optionList={tokenOptions}
                    filter
                    showClear
                    searchPosition='dropdown'
                    style={{ width: '100%' }}
                    placeholder={t('Select a probe token')}
                    onChange={(token_id) => updateDraft({ token_id })}
                  />
                </Col>
                <Col xs={24} md={6}>
                  <Button
                    className='w-full'
                    icon={<KeyRound size={14} />}
                    onClick={onManageTokens}
                  >
                    {t('Manage tokens')}
                  </Button>
                </Col>
              </Row>
              {tokenOptions.length === 0 && !optionsLoading && (
                <Banner
                  type='warning'
                  className='mt-2'
                  description={t(
                    'Mark an enabled token as a probe token before creating a local target.',
                  )}
                />
              )}
            </div>
          ) : (
            <Row gutter={[16, 16]}>
              <Col xs={24} md={12}>
                <Form.Input
                  label={t('Upstream HTTPS endpoint')}
                  placeholder={
                    target?.endpoint_masked
                      ? `${target.endpoint_masked} (${t('Leave blank to keep the saved endpoint.')})`
                      : 'https://api.example.com'
                  }
                  value={draft.endpoint}
                  onChange={(endpoint) => updateDraft({ endpoint }, false)}
                />
              </Col>
              <Col xs={24} md={12}>
                <Form.Input
                  label={t('Upstream API key')}
                  mode='password'
                  placeholder={
                    target?.has_key
                      ? t('Leave blank to keep the saved key.')
                      : 'sk-...'
                  }
                  value={draft.api_key}
                  onChange={(api_key) => updateDraft({ api_key }, false)}
                />
              </Col>
            </Row>
          )}

          <div className='mb-3 mt-6 text-sm font-semibold'>
            {t('Detection scope and public display')}
          </div>
          <Row gutter={[16, 16]}>
            <Col xs={24} md={12}>
              <Form.Select
                label={t('Observed traffic groups')}
                value={draft.observed_groups}
                optionList={adminOptions.groups.map((group) => ({
                  value: group.name,
                  label: group.name,
                }))}
                multiple
                filter
                showClear
                maxTagCount={3}
                searchPosition='dropdown'
                style={{ width: '100%' }}
                placeholder={t('No observed traffic groups')}
                extraText={t(
                  'Leave empty to show active probe results without observed traffic.',
                )}
                onChange={setObservedGroups}
              />
            </Col>
            <Col xs={24} md={12}>
              <Form.Select
                label={t('Models')}
                value={selectedModelNames}
                optionList={modelOptions}
                multiple
                filter
                allowCreate={draft.mode === 'upstream'}
                showClear
                maxTagCount={3}
                searchPosition='dropdown'
                style={{ width: '100%' }}
                placeholder={t('Select one or more models')}
                extraText={
                  draft.mode === 'upstream'
                    ? t('You can enter a custom upstream model name.')
                    : t('Models are limited to the selected groups.')
                }
                onChange={setSelectedModels}
              />
            </Col>
          </Row>

          {unknownModels.length > 0 && (
            <Banner
              type='warning'
              className='mb-3'
              description={t(
                'Saved custom models are kept even when they are not in the current site catalog.',
              )}
            />
          )}

          <Form.Input
            label={t('Public group alias')}
            value={draft.public_group_alias}
            onChange={(public_group_alias) => {
              setGroupAliasTouched(true);
              updateDraft({ public_group_alias }, false);
            }}
          />

          <Row gutter={[16, 16]}>
            <Col xs={24} md={12}>
              <Form.Select
                label={t('Observed traffic channels')}
                value={draft.observed_channel_ids}
                optionList={channelOptions}
                multiple
                filter
                showClear
                maxTagCount={3}
                searchPosition='dropdown'
                style={{ width: '100%' }}
                placeholder={t('Select one or more channels')}
                extraText={t(
                  'Channels are filtered by the selected traffic groups and models.',
                )}
                onChange={(values) =>
                  updateDraft(
                    {
                      observed_channel_ids: [
                        ...new Set(
                          (Array.isArray(values) ? values : [])
                            .map(Number)
                            .filter((id) => Number.isInteger(id) && id > 0),
                        ),
                      ].slice(0, 20),
                    },
                    false,
                  )
                }
              />
            </Col>
            <Col xs={24} md={12}>
              <Form.Input
                label={t('Public pool name')}
                value={draft.public_pool_alias}
                placeholder={t('Example: CCMAX-A pool')}
                onChange={(public_pool_alias) =>
                  updateDraft({ public_pool_alias }, false)
                }
              />
            </Col>
          </Row>

          {unavailableChannelIds.length > 0 && (
            <Banner
              type='warning'
              className='mb-3'
              description={t(
                'A saved channel binding is disabled or no longer available.',
              )}
            />
          )}

          <ModelPublicationRows
            models={draft.models}
            t={t}
            onChange={(models) => updateDraft({ models })}
          />

          <div className='mt-5 flex flex-wrap gap-6'>
            <label className='flex items-center gap-2'>
              <Switch
                checked={draft.enabled}
                onChange={(enabled) => updateDraft({ enabled }, false)}
              />
              <span>{t('Include this target in scheduled probes.')}</span>
            </label>
            <label className='flex items-center gap-2'>
              <Switch
                checked={draft.publish_pool_detail}
                onChange={(publish_pool_detail) =>
                  updateDraft({ publish_pool_detail }, false)
                }
              />
              <span>{t('Publish pool details')}</span>
            </label>
            <label className='flex items-center gap-2'>
              <Switch
                checked={draft.public}
                onChange={(value) => updateDraft({ public: value }, false)}
              />
              <span>
                {t(
                  'Allow its aliases and aggregate metrics on the health page.',
                )}
              </span>
            </label>
          </div>

          <Collapse className='mt-5'>
            <Collapse.Panel header={t('Advanced settings')} itemKey='advanced'>
              <Form.Input
                label={t('Internal target name')}
                value={draft.name}
                onChange={(name) => {
                  setNameTouched(true);
                  updateDraft({ name }, false);
                }}
              />
              <Row gutter={[16, 16]}>
                <Col xs={24} md={8}>
                  <Form.InputNumber
                    label={t('Interval (seconds)')}
                    min={60}
                    max={3600}
                    value={draft.interval_seconds}
                    onChange={(interval_seconds) =>
                      updateDraft({ interval_seconds }, false)
                    }
                  />
                </Col>
                <Col xs={24} md={8}>
                  <Form.InputNumber
                    label={t('Timeout (seconds)')}
                    min={1}
                    max={300}
                    value={draft.timeout_seconds}
                    onChange={(timeout_seconds) =>
                      updateDraft({ timeout_seconds }, false)
                    }
                  />
                </Col>
                <Col xs={24} md={8}>
                  <Form.InputNumber
                    label={t('Latency SLO (ms)')}
                    min={0}
                    value={draft.latency_slo_ms}
                    onChange={(latency_slo_ms) =>
                      updateDraft({ latency_slo_ms }, false)
                    }
                    extraText={t('0 disables latency downgrade')}
                  />
                </Col>
              </Row>

              <div className='mb-3 mt-5 text-sm font-semibold'>
                {t('Sampling strategy')}
              </div>
              {!multiSampleEnabled && (
                <Banner
                  type='warning'
                  className='mb-3'
                  description={t(
                    'Multi-sample probes are disabled globally; this target will run once per cycle.',
                  )}
                />
              )}
              <div className='mb-4'>
                <RadioGroup
                  type='button'
                  className='flex flex-wrap'
                  value={draft.sampling_mode}
                  onChange={(event) =>
                    updateDraft(
                      { sampling_mode: event?.target?.value ?? event },
                      false,
                    )
                  }
                >
                  <Radio value='fixed'>{t('Fixed samples')}</Radio>
                  <Radio value='confirm_on_failure'>
                    {t('Confirm on failure')}
                  </Radio>
                </RadioGroup>
                <div className='mt-2 text-xs text-semi-color-text-2'>
                  {draft.sampling_mode === 'fixed'
                    ? t('Run every sample in each cycle.')
                    : t(
                        'Stop after the first success; retry only after an initial failure.',
                      )}
                </div>
              </div>
              <Row gutter={[16, 16]}>
                <Col xs={24} md={8}>
                  <Form.InputNumber
                    label={t('Samples per run')}
                    min={1}
                    max={5}
                    value={draft.samples_per_run}
                    onChange={(value) => {
                      const samples = boundedInteger(value, 1, 1, 5);
                      updateDraft(
                        {
                          samples_per_run: samples,
                          minimum_successes:
                            samples === 1
                              ? 1
                              : Math.min(draft.minimum_successes, samples),
                        },
                        false,
                      );
                    }}
                  />
                </Col>
                <Col xs={24} md={8}>
                  <Form.InputNumber
                    label={t('Minimum successes')}
                    min={1}
                    max={draft.samples_per_run}
                    disabled={draft.samples_per_run === 1}
                    value={draft.minimum_successes}
                    onChange={(minimum_successes) =>
                      updateDraft({ minimum_successes }, false)
                    }
                  />
                </Col>
                <Col xs={24} md={8}>
                  <Form.InputNumber
                    label={t('Attempt spacing (seconds)')}
                    min={1}
                    max={30}
                    disabled={draft.samples_per_run === 1}
                    value={draft.sample_spacing_seconds}
                    onChange={(sample_spacing_seconds) =>
                      updateDraft({ sample_spacing_seconds }, false)
                    }
                  />
                </Col>
              </Row>
              {samplingEstimate.budgetExceeded && (
                <Banner
                  type='danger'
                  className='mb-3'
                  description={t(
                    'The sampling time budget exceeds the target interval.',
                  )}
                />
              )}
              <Banner
                type='info'
                description={
                  <div className='space-y-1'>
                    <div>
                      {draft.sampling_mode === 'fixed'
                        ? t('Fixed {{count}}x probe volume per cycle.', {
                            count: draft.samples_per_run,
                          })
                        : t(
                            'Healthy cycles use about 1x; failure cycles use up to {{count}}x.',
                            { count: draft.samples_per_run },
                          )}
                    </div>
                    <div>
                      {t(
                        'At most {{count}} attempts per day across {{models}} models.',
                        {
                          count:
                            samplingEstimate.maxAttemptsPerDay.toLocaleString(
                              locale,
                            ),
                          models: samplingEstimate.modelCount,
                        },
                      )}
                    </div>
                  </div>
                }
              />
            </Collapse.Panel>
          </Collapse>
          <ValidationResultPanel result={validationResult} t={t} />
        </Form>
      </Spin>
    </Modal>
  );
}

export default function SettingsModelHealth({ options, refresh }) {
  const { t, i18n } = useTranslation();
  const [settings, setSettings] = useState(SETTING_DEFAULTS);
  const [targets, setTargets] = useState([]);
  const [adminOptions, setAdminOptions] = useState(EMPTY_ADMIN_OPTIONS);
  const [probeTokens, setProbeTokens] = useState([]);
  const [loading, setLoading] = useState(false);
  const [targetsLoading, setTargetsLoading] = useState(false);
  const [optionsLoading, setOptionsLoading] = useState(false);
  const [probeTokensLoading, setProbeTokensLoading] = useState(false);
  const [updatingTokenId, setUpdatingTokenId] = useState(null);
  const [modalVisible, setModalVisible] = useState(false);
  const [tokenManagerVisible, setTokenManagerVisible] = useState(false);
  const [editingTarget, setEditingTarget] = useState(null);

  useEffect(() => {
    setSettings({
      'model_health_setting.enabled': toBoolean(
        options?.['model_health_setting.enabled'] ?? false,
      ),
      'model_health_setting.pool_details_enabled': toBoolean(
        options?.['model_health_setting.pool_details_enabled'] ?? false,
      ),
      'model_health_setting.multi_sample_enabled': toBoolean(
        options?.['model_health_setting.multi_sample_enabled'] ?? false,
      ),
      'model_health_setting.default_interval_seconds': optionNumber(
        options,
        'model_health_setting.default_interval_seconds',
      ),
      'model_health_setting.default_timeout_seconds': optionNumber(
        options,
        'model_health_setting.default_timeout_seconds',
      ),
      'model_health_setting.default_sampling_mode': samplingMode(
        options?.['model_health_setting.default_sampling_mode'],
        SETTING_DEFAULTS['model_health_setting.default_sampling_mode'],
      ),
      'model_health_setting.default_samples_per_run': optionNumber(
        options,
        'model_health_setting.default_samples_per_run',
      ),
      'model_health_setting.default_minimum_successes': optionNumber(
        options,
        'model_health_setting.default_minimum_successes',
      ),
      'model_health_setting.default_sample_spacing_seconds': optionNumber(
        options,
        'model_health_setting.default_sample_spacing_seconds',
      ),
      'model_health_setting.concurrency': optionNumber(
        options,
        'model_health_setting.concurrency',
      ),
      'model_health_setting.retention_days': optionNumber(
        options,
        'model_health_setting.retention_days',
      ),
      'model_health_setting.healthy_threshold': optionNumber(
        options,
        'model_health_setting.healthy_threshold',
      ),
      'model_health_setting.fluctuating_threshold': optionNumber(
        options,
        'model_health_setting.fluctuating_threshold',
      ),
      'model_health_setting.passive_min_samples': optionNumber(
        options,
        'model_health_setting.passive_min_samples',
      ),
      'model_health_setting.active_min_samples': optionNumber(
        options,
        'model_health_setting.active_min_samples',
      ),
      'perf_metrics_setting.enabled': toBoolean(
        options?.['perf_metrics_setting.enabled'] ?? true,
      ),
      'perf_metrics_setting.flush_interval': optionNumber(
        options,
        'perf_metrics_setting.flush_interval',
      ),
      'perf_metrics_setting.bucket_time':
        options?.['perf_metrics_setting.bucket_time'] || '5min',
      'perf_metrics_setting.retention_days': optionNumber(
        options,
        'perf_metrics_setting.retention_days',
      ),
    });
  }, [options]);

  const loadTargets = useCallback(async () => {
    setTargetsLoading(true);
    try {
      const response = await API.get('/api/model-health/admin/targets');
      const data = unwrapResponse(response);
      setTargets(Array.isArray(data) ? data : data.targets || []);
    } catch {
      showError(t('Health data unavailable'));
    } finally {
      setTargetsLoading(false);
    }
  }, [t]);

  const loadAdminOptions = useCallback(async () => {
    setOptionsLoading(true);
    try {
      const response = await API.get('/api/model-health/admin/options');
      setAdminOptions(normalizeAdminOptions(unwrapResponse(response)));
    } catch {
      setAdminOptions(EMPTY_ADMIN_OPTIONS);
      showError(t('Health configuration options unavailable'));
    } finally {
      setOptionsLoading(false);
    }
  }, [t]);

  const loadProbeTokens = useCallback(async () => {
    setProbeTokensLoading(true);
    try {
      const response = await API.get('/api/model-health/admin/probe-tokens');
      setProbeTokens(normalizeProbeTokens(unwrapResponse(response)));
    } catch {
      setProbeTokens([]);
      showError(t('Probe tokens unavailable'));
    } finally {
      setProbeTokensLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void Promise.all([loadTargets(), loadAdminOptions()]);
  }, [loadAdminOptions, loadTargets]);

  const setSetting = (key, value) =>
    setSettings((current) => ({ ...current, [key]: value }));

  const saveSettings = async () => {
    if (
      settings['model_health_setting.fluctuating_threshold'] >
      settings['model_health_setting.healthy_threshold']
    ) {
      showError(t('Fluctuating threshold must not exceed healthy threshold'));
      return;
    }
    const defaultSamples = boundedInteger(
      settings['model_health_setting.default_samples_per_run'],
      3,
      1,
      5,
    );
    if (
      Number(settings['model_health_setting.default_minimum_successes']) >
      defaultSamples
    ) {
      showError(t('Minimum successes must not exceed samples per run.'));
      return;
    }
    if (
      samplingBudgetExceeded({
        samples_per_run: defaultSamples,
        timeout_seconds:
          settings['model_health_setting.default_timeout_seconds'],
        sample_spacing_seconds:
          settings['model_health_setting.default_sample_spacing_seconds'],
        interval_seconds:
          settings['model_health_setting.default_interval_seconds'],
      })
    ) {
      showError(t('The sampling time budget exceeds the target interval.'));
      return;
    }
    setLoading(true);
    try {
      const responses = await Promise.all(
        Object.entries(settings).map(([key, value]) =>
          API.put('/api/option/', { key, value: String(value) }),
        ),
      );
      const failed = responses.find(
        (response) => response?.data?.success === false,
      );
      if (failed) {
        throw new Error(failed.data.message || t('Failed to save settings'));
      }
      showSuccess(t('Model health settings saved'));
      await refresh?.();
    } catch (error) {
      showError(
        error?.response?.data?.message ||
          error.message ||
          t('Failed to save settings'),
      );
    } finally {
      setLoading(false);
    }
  };

  const saveTarget = async (payload) => {
    setLoading(true);
    try {
      const response = editingTarget?.id
        ? await API.put(
            `/api/model-health/admin/targets/${editingTarget.id}`,
            payload,
          )
        : await API.post('/api/model-health/admin/targets', payload);
      if (response?.data?.success === false) {
        throw new Error(response.data.message);
      }
      showSuccess(t('Model health settings saved'));
      setModalVisible(false);
      setEditingTarget(null);
      await Promise.all([loadTargets(), loadAdminOptions()]);
    } catch (error) {
      showError(error?.response?.data?.message || error.message);
    } finally {
      setLoading(false);
    }
  };

  const validateTarget = async (payload) => {
    setLoading(true);
    try {
      const response = await API.post(
        '/api/model-health/admin/targets/validate',
        payload,
      );
      const data = unwrapResponse(response);
      if (response?.data?.success === false) {
        showError(response.data.message || t('Validation completed'));
        const batch = data?.batch ?? data?.result ?? data;
        return batch?.batch_status ||
          batch?.actual_attempts !== undefined ||
          Array.isArray(batch?.attempts)
          ? data
          : null;
      }
      showSuccess(t('Validation completed'));
      return data;
    } catch (error) {
      const responseData = error?.response?.data;
      if (responseData?.data) {
        showError(responseData.message || error.message);
        const data = responseData.data;
        const batch = data?.batch ?? data?.result ?? data;
        return batch?.batch_status ||
          batch?.actual_attempts !== undefined ||
          Array.isArray(batch?.attempts)
          ? data
          : null;
      }
      showError(responseData?.message || error.message);
      return null;
    } finally {
      setLoading(false);
    }
  };

  const deleteTarget = async (id) => {
    try {
      const response = await API.delete(
        `/api/model-health/admin/targets/${id}`,
      );
      if (response?.data?.success === false) {
        throw new Error(response.data.message);
      }
      showSuccess(t('Target deleted'));
      await Promise.all([
        loadTargets(),
        tokenManagerVisible ? loadProbeTokens() : Promise.resolve(),
      ]);
    } catch (error) {
      showError(error?.response?.data?.message || error.message);
    }
  };

  const runTarget = async (id) => {
    try {
      const response = await API.post(
        `/api/model-health/admin/targets/${id}/run`,
      );
      if (response?.data?.success === false) {
        throw new Error(response.data.message);
      }
      showSuccess(t('Probe task started'));
    } catch (error) {
      showError(error?.response?.data?.message || error.message);
    }
  };

  const runAll = async () => {
    try {
      const response = await API.post('/api/model-health/admin/run');
      if (response?.data?.success === false) {
        throw new Error(response.data.message);
      }
      showSuccess(t('Probe tasks started'));
    } catch (error) {
      showError(error?.response?.data?.message || error.message);
    }
  };

  const openTokenManager = () => {
    setTokenManagerVisible(true);
    void loadProbeTokens();
  };

  const toggleProbeToken = useCallback(
    async (token, marked) => {
      setUpdatingTokenId(token.id);
      try {
        const response = await API.put(
          `/api/model-health/admin/probe-tokens/${token.id}`,
          { marked },
        );
        if (response?.data?.success === false) {
          throw new Error(response.data.message);
        }
        showSuccess(
          marked ? t('Probe token enabled') : t('Probe token disabled'),
        );
        await Promise.all([loadProbeTokens(), loadAdminOptions()]);
      } catch (error) {
        showError(error?.response?.data?.message || error.message);
      } finally {
        setUpdatingTokenId(null);
      }
    },
    [loadAdminOptions, loadProbeTokens, t],
  );

  const columns = useMemo(
    () => [
      {
        title: t('Target'),
        dataIndex: 'name',
        render: (name, target) => (
          <div>
            <div className='font-medium'>{name}</div>
            <Text type='tertiary' size='small'>
              {target.public_group_alias || t('Private target')}
            </Text>
            {target.publish_pool_detail && target.public_pool_alias && (
              <div>
                <Tag color='blue' size='small'>
                  {target.public_pool_alias}
                </Tag>
              </div>
            )}
          </div>
        ),
      },
      {
        title: t('Mode'),
        dataIndex: 'mode',
        render: (mode) => (
          <Tag color={mode === 'local' ? 'blue' : 'purple'}>
            {mode === 'local' ? t('Local token') : t('Upstream HTTPS endpoint')}
          </Tag>
        ),
      },
      {
        title: t('Observed traffic groups'),
        render: (_, target) => {
          const groups = uniqueNames(
            target.observed_groups?.length
              ? target.observed_groups
              : target.observed_group
                ? [target.observed_group]
                : [],
          );
          return groups.length > 0 ? groups.join(', ') : '--';
        },
      },
      {
        title: t('Protocol'),
        dataIndex: 'protocol',
        render: (protocol) =>
          PROTOCOLS.find((item) => item.value === protocol)?.label || protocol,
      },
      {
        title: t('Models'),
        dataIndex: 'models',
        render: (models) => (Array.isArray(models) ? models.length : 0),
      },
      {
        title: t('Status'),
        dataIndex: 'enabled',
        render: (enabled, target) => (
          <Space>
            <Tag color={enabled ? 'green' : 'grey'}>
              {enabled ? t('Enabled') : t('Disabled')}
            </Tag>
            {target.public && <Tag color='cyan'>{t('Public')}</Tag>}
          </Space>
        ),
      },
      {
        title: t('Credentials'),
        dataIndex: 'credential_fingerprint',
        render: (fingerprint, target) =>
          target.mode === 'upstream' ? (
            <Text code>
              {fingerprint || (target.has_key ? '••••••••' : '--')}
            </Text>
          ) : (
            <Text type='tertiary'>
              {adminOptions.tokens.find(
                (token) => token.id === Number(target.token_id),
              )?.name || `#${target.token_id}`}
            </Text>
          ),
      },
      {
        title: t('Actions'),
        fixed: 'right',
        width: 190,
        render: (_, target) => (
          <Space>
            <Button
              size='small'
              icon={<Play size={13} />}
              onClick={() => void runTarget(target.id)}
            />
            <Button
              size='small'
              icon={<Edit size={13} />}
              onClick={() => {
                setEditingTarget(target);
                setModalVisible(true);
              }}
            />
            <Popconfirm
              title={t('Delete this target and its health history?')}
              onConfirm={() => void deleteTarget(target.id)}
            >
              <Button size='small' type='danger' icon={<Trash2 size={13} />} />
            </Popconfirm>
          </Space>
        ),
      },
    ],
    [adminOptions.tokens, t],
  );

  const defaultTargetSettings = useMemo(
    () => ({
      intervalSeconds:
        settings['model_health_setting.default_interval_seconds'],
      timeoutSeconds: settings['model_health_setting.default_timeout_seconds'],
      samplingMode:
        settings['model_health_setting.default_sampling_mode'] ||
        SETTING_DEFAULTS['model_health_setting.default_sampling_mode'],
      samplesPerRun: boundedInteger(
        settings['model_health_setting.default_samples_per_run'],
        3,
        1,
        5,
      ),
      minimumSuccesses: boundedInteger(
        settings['model_health_setting.default_minimum_successes'],
        2,
        1,
        boundedInteger(
          settings['model_health_setting.default_samples_per_run'],
          3,
          1,
          5,
        ),
      ),
      sampleSpacingSeconds: boundedInteger(
        settings['model_health_setting.default_sample_spacing_seconds'],
        3,
        1,
        30,
      ),
    }),
    [settings],
  );

  return (
    <div className='space-y-4'>
      <Card className='!rounded-xl'>
        <div className='mb-4 flex flex-wrap items-start justify-between gap-3'>
          <div>
            <div className='flex items-center gap-2'>
              <Activity size={18} className='text-semi-color-primary' />
              <Title heading={5}>{t('Model health')}</Title>
            </div>
            <Text type='tertiary'>
              {t(
                'Use a local token or connect directly to an upstream endpoint.',
              )}
            </Text>
          </div>
          <Button
            type='primary'
            icon={<Save size={14} />}
            loading={loading}
            onClick={() => void saveSettings()}
          >
            {t('Save settings')}
          </Button>
        </div>

        <Banner
          type='info'
          className='mb-4'
          description={t(
            'Public models, groups, and aliases are managed on each probe target.',
          )}
        />

        <div className='mb-5 flex items-center gap-3'>
          <Switch
            checked={settings['model_health_setting.enabled']}
            onChange={(value) =>
              setSetting('model_health_setting.enabled', value)
            }
          />
          <div>
            <div className='font-medium'>
              {t('Enable scheduled health probes')}
            </div>
            <Text type='tertiary' size='small'>
              {t('Targets remain saved while scheduled collection is paused.')}
            </Text>
          </div>
        </div>

        <div className='mb-5 flex items-center gap-3'>
          <Switch
            checked={settings['model_health_setting.pool_details_enabled']}
            onChange={(value) =>
              setSetting('model_health_setting.pool_details_enabled', value)
            }
          />
          <div>
            <div className='font-medium'>{t('Enable public pool details')}</div>
            <Text type='tertiary' size='small'>
              {t(
                'Channel metrics continue collecting while public pool details are hidden.',
              )}
            </Text>
          </div>
        </div>

        <div className='mb-5 flex items-center gap-3'>
          <Switch
            checked={settings['model_health_setting.multi_sample_enabled']}
            onChange={(value) =>
              setSetting('model_health_setting.multi_sample_enabled', value)
            }
          />
          <div>
            <div className='font-medium'>
              {t('Enable multiple probes per cycle')}
            </div>
            <Text type='tertiary' size='small'>
              {t(
                'When disabled, every target runs one probe per scheduled cycle.',
              )}
            </Text>
          </div>
        </div>

        <Row gutter={[16, 16]}>
          <Col xs={24} sm={12} md={8}>
            <label className='mb-1 block text-sm font-medium'>
              {t('Default interval (seconds)')}
            </label>
            <InputNumber
              className='w-full'
              min={60}
              max={3600}
              value={settings['model_health_setting.default_interval_seconds']}
              onChange={(value) =>
                setSetting(
                  'model_health_setting.default_interval_seconds',
                  value,
                )
              }
            />
          </Col>
          <Col xs={24} sm={12} md={8}>
            <label className='mb-1 block text-sm font-medium'>
              {t('Default timeout (seconds)')}
            </label>
            <InputNumber
              className='w-full'
              min={1}
              max={300}
              value={settings['model_health_setting.default_timeout_seconds']}
              onChange={(value) =>
                setSetting(
                  'model_health_setting.default_timeout_seconds',
                  value,
                )
              }
            />
          </Col>
          <Col xs={24} md={8}>
            <label className='flex h-full min-h-8 items-center gap-2 pt-6 md:pt-0'>
              <Switch
                checked={settings['perf_metrics_setting.enabled']}
                onChange={(value) =>
                  setSetting('perf_metrics_setting.enabled', value)
                }
              />
              <span>{t('Enable model performance metrics')}</span>
            </label>
          </Col>
        </Row>

        <div className='mb-3 mt-5 text-sm font-semibold'>
          {t('New targets use these sampling defaults.')}
        </div>
        <div className='mb-4'>
          <label className='mb-1 block text-sm font-medium'>
            {t('Default sampling mode')}
          </label>
          <RadioGroup
            type='button'
            className='flex flex-wrap'
            value={settings['model_health_setting.default_sampling_mode']}
            onChange={(event) =>
              setSetting(
                'model_health_setting.default_sampling_mode',
                event?.target?.value ?? event,
              )
            }
          >
            <Radio value='fixed'>{t('Fixed samples')}</Radio>
            <Radio value='confirm_on_failure'>{t('Confirm on failure')}</Radio>
          </RadioGroup>
        </div>
        <Row gutter={[16, 16]}>
          <Col xs={24} sm={12} md={8}>
            <label className='mb-1 block text-sm font-medium'>
              {t('Samples per run')}
            </label>
            <InputNumber
              className='w-full'
              min={1}
              max={5}
              value={settings['model_health_setting.default_samples_per_run']}
              onChange={(value) => {
                const samples = boundedInteger(value, 1, 1, 5);
                setSettings((current) => ({
                  ...current,
                  'model_health_setting.default_samples_per_run': samples,
                  'model_health_setting.default_minimum_successes':
                    samples === 1
                      ? 1
                      : Math.min(
                          current[
                            'model_health_setting.default_minimum_successes'
                          ],
                          samples,
                        ),
                }));
              }}
            />
          </Col>
          <Col xs={24} sm={12} md={8}>
            <label className='mb-1 block text-sm font-medium'>
              {t('Minimum successes')}
            </label>
            <InputNumber
              className='w-full'
              min={1}
              max={settings['model_health_setting.default_samples_per_run']}
              disabled={
                settings['model_health_setting.default_samples_per_run'] === 1
              }
              value={settings['model_health_setting.default_minimum_successes']}
              onChange={(value) =>
                setSetting(
                  'model_health_setting.default_minimum_successes',
                  value,
                )
              }
            />
          </Col>
          <Col xs={24} sm={12} md={8}>
            <label className='mb-1 block text-sm font-medium'>
              {t('Attempt spacing (seconds)')}
            </label>
            <InputNumber
              className='w-full'
              min={1}
              max={30}
              disabled={
                settings['model_health_setting.default_samples_per_run'] === 1
              }
              value={
                settings['model_health_setting.default_sample_spacing_seconds']
              }
              onChange={(value) =>
                setSetting(
                  'model_health_setting.default_sample_spacing_seconds',
                  value,
                )
              }
            />
          </Col>
        </Row>
        {samplingBudgetExceeded({
          samples_per_run:
            settings['model_health_setting.default_samples_per_run'],
          timeout_seconds:
            settings['model_health_setting.default_timeout_seconds'],
          sample_spacing_seconds:
            settings['model_health_setting.default_sample_spacing_seconds'],
          interval_seconds:
            settings['model_health_setting.default_interval_seconds'],
        }) && (
          <Banner
            type='danger'
            className='mt-3'
            description={t(
              'The sampling time budget exceeds the target interval.',
            )}
          />
        )}

        <Collapse className='mt-5'>
          <Collapse.Panel header={t('Advanced settings')} itemKey='advanced'>
            <Row gutter={[16, 16]}>
              {[
                ['concurrency', 'Concurrent probes', 1, 32],
                ['retention_days', 'History retention (days)', 1, 365],
                ['healthy_threshold', 'Healthy threshold (%)', 1, 100],
                ['fluctuating_threshold', 'Fluctuating threshold (%)', 1, 100],
                ['active_min_samples', 'Active minimum samples', 1, 1000],
                [
                  'passive_min_samples',
                  'Observed minimum requests',
                  1,
                  1000000,
                ],
              ].map(([key, label, min, max]) => (
                <Col xs={24} sm={12} md={8} key={key}>
                  <label className='mb-1 block text-sm font-medium'>
                    {t(label)}
                  </label>
                  <InputNumber
                    className='w-full'
                    min={min}
                    max={max}
                    value={settings[`model_health_setting.${key}`]}
                    onChange={(value) =>
                      setSetting(`model_health_setting.${key}`, value)
                    }
                  />
                </Col>
              ))}
            </Row>

            <div className='mb-3 mt-5 text-sm font-semibold'>
              {t('Observed performance metrics')}
            </div>
            <Row gutter={[16, 16]}>
              <Col xs={24} md={8}>
                <label className='mb-1 block text-sm font-medium'>
                  {t('Flush interval (minutes)')}
                </label>
                <InputNumber
                  className='w-full'
                  min={1}
                  value={settings['perf_metrics_setting.flush_interval']}
                  onChange={(value) =>
                    setSetting('perf_metrics_setting.flush_interval', value)
                  }
                />
              </Col>
              <Col xs={24} md={8}>
                <label className='mb-1 block text-sm font-medium'>
                  {t('Aggregation bucket')}
                </label>
                <Select
                  className='w-full'
                  value={settings['perf_metrics_setting.bucket_time']}
                  onChange={(value) =>
                    setSetting('perf_metrics_setting.bucket_time', value)
                  }
                  optionList={[
                    { value: 'minute', label: t('1 minute') },
                    { value: '5min', label: t('5 minutes') },
                    { value: 'hour', label: t('1 hour') },
                  ]}
                />
              </Col>
              <Col xs={24} md={8}>
                <label className='mb-1 block text-sm font-medium'>
                  {t('Retention days')}
                </label>
                <InputNumber
                  className='w-full'
                  min={0}
                  value={settings['perf_metrics_setting.retention_days']}
                  onChange={(value) =>
                    setSetting('perf_metrics_setting.retention_days', value)
                  }
                />
              </Col>
            </Row>
          </Collapse.Panel>
        </Collapse>
      </Card>

      <Card className='!rounded-xl'>
        <div className='mb-4 flex flex-wrap items-center justify-between gap-3'>
          <div>
            <Title heading={5}>{t('Probe targets')}</Title>
            <Text type='tertiary'>
              {t('Required models determine the target rollup status.')}
            </Text>
          </div>
          <Space wrap>
            <Button icon={<KeyRound size={14} />} onClick={openTokenManager}>
              {t('Manage probe tokens')}
            </Button>
            <Button icon={<Play size={14} />} onClick={() => void runAll()}>
              {t('Run all')}
            </Button>
            <Button
              type='primary'
              icon={<Plus size={14} />}
              onClick={() => {
                setEditingTarget(null);
                setModalVisible(true);
              }}
            >
              {t('Add target')}
            </Button>
          </Space>
        </div>
        <Table
          columns={columns}
          dataSource={targets}
          rowKey='id'
          loading={targetsLoading}
          pagination={false}
          scroll={{ x: 1250 }}
        />
      </Card>

      <TargetModal
        visible={modalVisible}
        target={editingTarget}
        defaults={defaultTargetSettings}
        adminOptions={adminOptions}
        optionsLoading={optionsLoading}
        saving={loading}
        onCancel={() => {
          setModalVisible(false);
          setEditingTarget(null);
        }}
        onSave={saveTarget}
        onValidate={validateTarget}
        onManageTokens={openTokenManager}
        t={t}
        locale={i18n.resolvedLanguage || i18n.language}
        multiSampleEnabled={
          settings['model_health_setting.multi_sample_enabled']
        }
      />

      <ProbeTokenManagerModal
        visible={tokenManagerVisible}
        loading={probeTokensLoading}
        tokens={probeTokens}
        updatingId={updatingTokenId}
        onToggle={toggleProbeToken}
        onCancel={() => setTokenManagerVisible(false)}
        t={t}
        locale={i18n.resolvedLanguage || i18n.language}
      />
    </div>
  );
}
