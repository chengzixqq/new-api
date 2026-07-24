/*
Copyright (C) 2023-2026 QuantumNous

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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronDown, KeyRound, Play, Plus, ShieldCheck } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useFieldArray, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { StaticRowActions } from '@/components/data-table/static/static-row-actions'
import { Dialog } from '@/components/dialog'
import { MultiSelect } from '@/components/multi-select'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Combobox } from '@/components/ui/combobox'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import {
  createModelHealthTarget,
  deleteModelHealthTarget,
  getModelHealthAdminOptions,
  getModelHealthProbeTokens,
  getModelHealthTargets,
  runAllModelHealthTargets,
  runModelHealthTarget,
  setModelHealthProbeToken,
  updateModelHealthTarget,
  validateModelHealthTarget,
} from '@/features/model-health/api'
import {
  automaticGroupAlias,
  automaticTargetName,
  hasPublicPoolAliasConflict,
  modelHealthChannelLabel,
  modelHealthGroupLabel,
  modelsAllowedByProbeToken,
  selectableModelsForGroups,
  selectableChannelsForScope,
  syncTargetModels,
} from '@/features/model-health/target-options'
import type {
  ModelHealthAdminOptions,
  ModelHealthProtocol,
  ModelHealthProbeToken,
  ModelHealthSamplingMode,
  ModelHealthTarget,
  ModelHealthTargetPayload,
  ModelHealthTargetValidationResult,
} from '@/features/model-health/types'
import { formatQuota } from '@/lib/format'

import {
  SettingsControlGroup,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { safeNumberFieldProps } from '../utils/numeric-field'

const PROTOCOLS: Array<{ value: ModelHealthProtocol; label: string }> = [
  { value: 'openai_chat', label: 'OpenAI Chat Completions' },
  { value: 'openai_responses', label: 'OpenAI Responses' },
  { value: 'anthropic_messages', label: 'Anthropic Messages' },
  { value: 'gemini_generate_content', label: 'Gemini GenerateContent' },
]

const createTargetSchema = (
  t: (key: string) => string,
  canKeepCredentials: boolean,
  targets: ModelHealthTarget[],
  currentTargetID?: number
) =>
  z
    .object({
      name: z.string().trim().min(1).max(128),
      mode: z.enum(['local', 'upstream']),
      token_id: z.string(),
      protocol: z.enum([
        'openai_chat',
        'openai_responses',
        'anthropic_messages',
        'gemini_generate_content',
      ]),
      endpoint: z.string(),
      api_key: z.string(),
      public_group_alias: z.string().trim().max(128),
      public_pool_alias: z.string().trim().max(128),
      publish_pool_detail: z.boolean(),
      observed_groups: z.array(z.string().trim().min(1).max(64)).max(20),
      observed_channel_ids: z.array(z.string()).max(20),
      enabled: z.boolean(),
      public: z.boolean(),
      interval_seconds: z.coerce.number().int().min(60).max(3600),
      timeout_seconds: z.coerce.number().int().min(1).max(300),
      latency_slo_ms: z.string(),
      sampling_mode: z.enum(['fixed', 'confirm_on_failure']),
      samples_per_run: z.coerce.number().int().min(1).max(5),
      minimum_successes: z.coerce.number().int().min(1).max(5),
      sample_spacing_seconds: z.coerce.number().int().min(1).max(30),
      models: z
        .array(
          z.object({
            name: z.string().trim().min(1).max(128),
            public_alias: z.string().trim().max(128),
            required: z.boolean(),
          })
        )
        .min(1, t('Select at least one model'))
        .max(20, t('A target supports up to 20 models')),
    })
    .superRefine((value, context) => {
      if (value.minimum_successes > value.samples_per_run) {
        context.addIssue({
          code: 'custom',
          path: ['minimum_successes'],
          message: t('Minimum successes cannot exceed samples per run'),
        })
      }
      const maximumRunSeconds =
        value.samples_per_run * value.timeout_seconds +
        (value.samples_per_run - 1) * value.sample_spacing_seconds
      if (maximumRunSeconds > value.interval_seconds) {
        context.addIssue({
          code: 'custom',
          path: ['samples_per_run'],
          message: t(
            'The maximum sampling duration must fit within the probe interval'
          ),
        })
      }
      if (
        (value.public || value.publish_pool_detail) &&
        !value.public_group_alias
      ) {
        context.addIssue({
          code: 'custom',
          path: ['public_group_alias'],
          message: t('Public group alias is required for public targets'),
        })
      }
      if (value.publish_pool_detail && !value.public_pool_alias) {
        context.addIssue({
          code: 'custom',
          path: ['public_pool_alias'],
          message: t(
            'Public pool name is required when pool details are published'
          ),
        })
      }
      if (
        value.publish_pool_detail &&
        hasPublicPoolAliasConflict(
          targets,
          currentTargetID,
          value.public_group_alias,
          value.public_pool_alias
        )
      ) {
        context.addIssue({
          code: 'custom',
          path: ['public_pool_alias'],
          message: t('Public pool name must be unique within its public group'),
        })
      }
      if (
        value.publish_pool_detail &&
        value.observed_channel_ids.length === 0
      ) {
        context.addIssue({
          code: 'custom',
          path: ['observed_channel_ids'],
          message: t('Select at least one channel for published pool details'),
        })
      }
      if (value.mode === 'local') {
        const tokenId = Number(value.token_id)
        if (!Number.isInteger(tokenId) || tokenId <= 0) {
          context.addIssue({
            code: 'custom',
            path: ['token_id'],
            message: t('Token ID is required'),
          })
        }
        return
      }
      if (!canKeepCredentials && !value.endpoint.trim()) {
        context.addIssue({
          code: 'custom',
          path: ['endpoint'],
          message: t('Enter a public HTTPS URL'),
        })
      }
      if (!canKeepCredentials && !value.api_key.trim()) {
        context.addIssue({
          code: 'custom',
          path: ['api_key'],
          message: t('API key is required'),
        })
      }
      if (value.endpoint.trim()) {
        try {
          const parsed = new URL(value.endpoint)
          if (parsed.protocol !== 'https:') throw new Error('https')
        } catch {
          context.addIssue({
            code: 'custom',
            path: ['endpoint'],
            message: t('Enter a public HTTPS URL'),
          })
        }
      }
    })

type TargetFormInput = z.input<ReturnType<typeof createTargetSchema>>
type TargetFormValues = z.output<ReturnType<typeof createTargetSchema>>

function emptyTarget(
  intervalSeconds: number,
  timeoutSeconds: number,
  samplingMode: ModelHealthSamplingMode,
  samplesPerRun: number,
  minimumSuccesses: number,
  sampleSpacingSeconds: number
): TargetFormInput {
  return {
    name: '',
    mode: 'local',
    token_id: '',
    protocol: 'openai_chat',
    endpoint: '',
    api_key: '',
    public_group_alias: '',
    public_pool_alias: '',
    publish_pool_detail: false,
    observed_groups: [],
    observed_channel_ids: [],
    enabled: true,
    public: false,
    interval_seconds: intervalSeconds,
    timeout_seconds: timeoutSeconds,
    latency_slo_ms: '',
    sampling_mode: samplingMode,
    samples_per_run: samplesPerRun,
    minimum_successes: minimumSuccesses,
    sample_spacing_seconds: sampleSpacingSeconds,
    models: [],
  }
}

function targetToForm(target: ModelHealthTarget): TargetFormInput {
  return {
    name: target.name,
    mode: target.mode,
    token_id: target.token_id ? String(target.token_id) : '',
    protocol: target.protocol,
    endpoint: '',
    api_key: '',
    public_group_alias: target.public_group_alias,
    public_pool_alias: target.public_pool_alias ?? '',
    publish_pool_detail: target.publish_pool_detail ?? false,
    observed_groups:
      target.observed_groups ??
      (target.observed_group ? [target.observed_group] : []),
    observed_channel_ids: (target.observed_channel_ids ?? []).map(String),
    enabled: target.enabled,
    public: target.public,
    interval_seconds: target.interval_seconds,
    timeout_seconds: target.timeout_seconds,
    latency_slo_ms: target.latency_slo_ms ? String(target.latency_slo_ms) : '',
    sampling_mode: target.sampling_mode ?? 'fixed',
    samples_per_run: target.samples_per_run ?? 1,
    minimum_successes: target.minimum_successes ?? 1,
    sample_spacing_seconds: target.sample_spacing_seconds ?? 3,
    models: target.models.map((model) => ({
      ...model,
      public_alias: model.public_alias || model.name,
    })),
  }
}

function formToPayload(values: TargetFormValues): ModelHealthTargetPayload {
  const latencySlo = values.latency_slo_ms.trim()
  return {
    name: values.name.trim(),
    mode: values.mode,
    token_id: values.mode === 'local' ? Number(values.token_id) : null,
    protocol: values.protocol,
    endpoint: values.mode === 'upstream' ? values.endpoint.trim() : '',
    api_key:
      values.mode === 'upstream' && values.api_key.trim()
        ? values.api_key.trim()
        : undefined,
    public_group_alias: values.public_group_alias.trim(),
    public_pool_alias: values.public_pool_alias.trim(),
    publish_pool_detail: values.publish_pool_detail,
    observed_groups: [...new Set(values.observed_groups)],
    observed_group: values.observed_groups[0] ?? '',
    observed_channel_ids: [...new Set(values.observed_channel_ids.map(Number))],
    enabled: values.enabled,
    public: values.public,
    interval_seconds: values.interval_seconds,
    timeout_seconds: values.timeout_seconds,
    latency_slo_ms: latencySlo ? Number(latencySlo) : null,
    sampling_mode: values.sampling_mode,
    samples_per_run: values.samples_per_run,
    minimum_successes:
      values.samples_per_run === 1 ? 1 : values.minimum_successes,
    sample_spacing_seconds: values.sample_spacing_seconds,
    models: values.models.map((model) => ({
      name: model.name.trim(),
      public_alias: model.public_alias.trim(),
      required: model.required,
    })),
  }
}

export function ModelHealthTargets(props: {
  defaultIntervalSeconds: number
  defaultTimeoutSeconds: number
  multiSampleEnabled: boolean
  defaultSamplingMode: ModelHealthSamplingMode
  defaultSamplesPerRun: number
  defaultMinimumSuccesses: number
  defaultSampleSpacingSeconds: number
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingTarget, setEditingTarget] = useState<ModelHealthTarget | null>(
    null
  )
  const [deleteTarget, setDeleteTarget] = useState<ModelHealthTarget | null>(
    null
  )
  const [probeTokensOpen, setProbeTokensOpen] = useState(false)
  const targetsQuery = useQuery({
    queryKey: ['model-health-admin-targets'],
    queryFn: getModelHealthTargets,
  })
  const optionsQuery = useQuery({
    queryKey: ['model-health-admin-options'],
    queryFn: getModelHealthAdminOptions,
  })
  const targets = targetsQuery.data?.data ?? []

  const showMutationError = (error: unknown) => {
    toast.error(
      error instanceof Error && error.message
        ? error.message
        : t('Operation failed')
    )
  }

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({
        queryKey: ['model-health-admin-targets'],
      }),
      queryClient.invalidateQueries({
        queryKey: ['model-health-probe-tokens'],
      }),
    ])
  }

  const saveMutation = useMutation({
    mutationFn: async (payload: ModelHealthTargetPayload) =>
      editingTarget
        ? updateModelHealthTarget(editingTarget.id, payload)
        : createModelHealthTarget(payload),
    onSuccess: async () => {
      toast.success(editingTarget ? t('Target updated') : t('Target created'))
      setDialogOpen(false)
      await refresh()
    },
    onError: showMutationError,
  })
  const validateMutation = useMutation({
    mutationFn: validateModelHealthTarget,
    onSuccess: (response) => {
      const result = response.data
      if (result.batch_status === 'healthy') {
        toast.success(t('Validation succeeded'))
      } else if (result.batch_status === 'fluctuating') {
        toast.warning(t('Validation completed with partial failures'))
      } else {
        toast.error(t('Validation completed with failures'))
      }
    },
    onError: showMutationError,
  })
  const runMutation = useMutation({
    mutationFn: runModelHealthTarget,
    onSuccess: () => {
      toast.success(t('Probe task started'))
    },
    onError: showMutationError,
  })
  const runAllMutation = useMutation({
    mutationFn: runAllModelHealthTargets,
    onSuccess: () => {
      toast.success(t('Probe tasks started'))
    },
    onError: showMutationError,
  })
  const deleteMutation = useMutation({
    mutationFn: deleteModelHealthTarget,
    onSuccess: async () => {
      toast.success(t('Target deleted'))
      setDeleteTarget(null)
      await refresh()
    },
    onError: showMutationError,
  })

  const openCreate = () => {
    validateMutation.reset()
    setEditingTarget(null)
    setDialogOpen(true)
  }
  const openEdit = (target: ModelHealthTarget) => {
    validateMutation.reset()
    setEditingTarget(target)
    setDialogOpen(true)
  }

  return (
    <div className='space-y-4'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div>
          <h3 className='font-semibold'>{t('Probe targets')}</h3>
          <p className='text-muted-foreground mt-1 text-xs'>
            {t(
              'Use a local token or connect directly to an upstream endpoint.'
            )}
          </p>
        </div>
        <div className='flex gap-2'>
          <Button
            size='sm'
            variant='outline'
            disabled={runAllMutation.isPending || targets.length === 0}
            onClick={() => runAllMutation.mutate()}
          >
            <Play className='size-4' />
            {t('Run all')}
          </Button>
          <Button size='sm' onClick={openCreate}>
            <Plus className='size-4' />
            {t('Add target')}
          </Button>
        </div>
      </div>

      <StaticDataTable
        data={targets}
        getRowKey={(target) => target.id}
        emptyContent={
          targetsQuery.isLoading
            ? t('Loading...')
            : t('No probe targets configured')
        }
        columns={[
          {
            id: 'name',
            header: t('Name'),
            cellClassName: 'font-medium',
            cell: (target) => (
              <div>
                <div>{target.name}</div>
                <div className='text-muted-foreground mt-0.5 font-mono text-xs'>
                  {target.public_group_alias}
                </div>
                {target.publish_pool_detail && target.public_pool_alias && (
                  <div className='text-muted-foreground mt-0.5 text-xs'>
                    {t('Pool')}: {target.public_pool_alias}
                  </div>
                )}
                <div className='text-muted-foreground mt-0.5 max-w-64 truncate font-mono text-[11px]'>
                  {target.mode === 'local'
                    ? `token:${target.token_id ?? '—'}`
                    : target.endpoint_masked || '—'}
                  {target.credential_fingerprint
                    ? ` · ${target.credential_fingerprint}`
                    : ''}
                </div>
              </div>
            ),
          },
          {
            id: 'mode',
            header: t('Mode'),
            cell: (target) => (
              <Badge variant='outline'>
                {target.mode === 'local' ? t('Local') : t('Upstream')}
              </Badge>
            ),
          },
          {
            id: 'protocol',
            header: t('Protocol'),
            cellClassName: 'text-muted-foreground text-xs',
            cell: (target) =>
              PROTOCOLS.find((item) => item.value === target.protocol)?.label ??
              target.protocol,
          },
          {
            id: 'models',
            header: t('Models'),
            cell: (target) =>
              t('{{count}} models', { count: target.models.length }),
          },
          {
            id: 'state',
            header: t('Status'),
            cell: (target) => (
              <div className='flex flex-wrap gap-1'>
                <Badge variant={target.enabled ? 'default' : 'secondary'}>
                  {target.enabled ? t('Enabled') : t('Disabled')}
                </Badge>
                {target.public && (
                  <Badge variant='outline'>{t('Public')}</Badge>
                )}
                {target.credentials_need_replacement && (
                  <Badge variant='destructive'>
                    {t('Configuration required')}
                  </Badge>
                )}
              </div>
            ),
          },
          {
            id: 'last',
            header: t('Last checked'),
            cellClassName: 'text-muted-foreground text-xs',
            cell: (target) =>
              target.last_checked_at
                ? new Date(target.last_checked_at).toLocaleString()
                : '—',
          },
          {
            id: 'actions',
            header: t('Actions'),
            cell: (target) => (
              <div className='flex items-center justify-end gap-1'>
                <Button
                  size='icon-sm'
                  variant='ghost'
                  aria-label={t('Run now')}
                  disabled={runMutation.isPending}
                  onClick={() => runMutation.mutate(target.id)}
                >
                  <Play />
                </Button>
                <StaticRowActions
                  editLabel={t('Edit')}
                  deleteLabel={t('Delete')}
                  menuLabel={t('Open menu')}
                  onEdit={() => openEdit(target)}
                  onDelete={() => setDeleteTarget(target)}
                />
              </div>
            ),
          },
        ]}
      />

      <TargetDialog
        open={dialogOpen}
        target={editingTarget}
        targets={targets}
        options={optionsQuery.data?.data}
        optionsLoading={optionsQuery.isLoading}
        optionsError={optionsQuery.isError}
        defaultIntervalSeconds={props.defaultIntervalSeconds}
        defaultTimeoutSeconds={props.defaultTimeoutSeconds}
        multiSampleEnabled={props.multiSampleEnabled}
        defaultSamplingMode={props.defaultSamplingMode}
        defaultSamplesPerRun={props.defaultSamplesPerRun}
        defaultMinimumSuccesses={props.defaultMinimumSuccesses}
        defaultSampleSpacingSeconds={props.defaultSampleSpacingSeconds}
        saving={saveMutation.isPending}
        validating={validateMutation.isPending}
        validationResult={validateMutation.data?.data}
        onOpenChange={(open) => {
          setDialogOpen(open)
          if (!open) validateMutation.reset()
        }}
        onManageProbeTokens={() => setProbeTokensOpen(true)}
        onSave={(payload) => saveMutation.mutate(payload)}
        onValidate={(payload) => validateMutation.mutate(payload)}
      />

      <ProbeTokenDialog
        open={probeTokensOpen}
        onOpenChange={setProbeTokensOpen}
      />

      <AlertDialog
        open={deleteTarget != null}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Delete target')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('Delete this target and its health history?')}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction
              variant='destructive'
              disabled={deleteMutation.isPending}
              onClick={() =>
                deleteTarget && deleteMutation.mutate(deleteTarget.id)
              }
            >
              {t('Delete')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function formatProbeTokenLabel(
  token: ModelHealthProbeToken | ModelHealthAdminOptions['tokens'][number],
  t: (key: string, options?: Record<string, unknown>) => string
): string {
  const group = token.group || t('Default group')
  const quota = token.unlimited_quota
    ? t('Unlimited')
    : formatQuota(token.remain_quota)
  const expires =
    token.expired_time > 0
      ? new Date(token.expired_time * 1000).toLocaleDateString()
      : t('Never expires')
  return `${token.name} · ${group} · ${quota} · ${expires}`
}

function ProbeTokenDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const tokensQuery = useQuery({
    queryKey: ['model-health-probe-tokens'],
    queryFn: getModelHealthProbeTokens,
    enabled: props.open,
  })
  const tokens = tokensQuery.data?.data ?? []
  let emptyContent = t('No tokens available')
  if (tokensQuery.isLoading) {
    emptyContent = t('Loading...')
  } else if (tokensQuery.isError) {
    emptyContent = t('Failed to load')
  }
  const markMutation = useMutation({
    mutationFn: ({ id, marked }: { id: number; marked: boolean }) =>
      setModelHealthProbeToken(id, marked),
    onSuccess: async () => {
      toast.success(t('Probe token setting updated'))
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: ['model-health-probe-tokens'],
        }),
        queryClient.invalidateQueries({
          queryKey: ['model-health-admin-options'],
        }),
      ])
    },
    onError: (error: unknown) => {
      toast.error(
        error instanceof Error && error.message
          ? error.message
          : t('Operation failed')
      )
    },
  })

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Manage probe tokens')}
      description={t(
        'Only marked tokens can be selected by local health probe targets.'
      )}
      contentHeight='min(65vh, 40rem)'
      contentClassName='sm:max-w-3xl'
      footer={
        <Button variant='outline' onClick={() => props.onOpenChange(false)}>
          {t('Close')}
        </Button>
      }
    >
      <StaticDataTable
        data={tokens}
        getRowKey={(token) => token.id}
        emptyContent={emptyContent}
        columns={[
          {
            id: 'name',
            header: t('Token'),
            cell: (token) => (
              <div className='min-w-0'>
                <p className='truncate font-medium' title={token.name}>
                  {token.name}
                </p>
                <p className='text-muted-foreground text-xs'>#{token.id}</p>
              </div>
            ),
          },
          {
            id: 'group',
            header: t('Group'),
            cell: (token) => token.group || t('Default group'),
          },
          {
            id: 'quota',
            header: t('Remaining quota'),
            cell: (token) =>
              token.unlimited_quota
                ? t('Unlimited')
                : formatQuota(token.remain_quota),
          },
          {
            id: 'expires',
            header: t('Expires'),
            cell: (token) =>
              token.expired_time > 0
                ? new Date(token.expired_time * 1000).toLocaleDateString()
                : t('Never expires'),
          },
          {
            id: 'references',
            header: t('Used by targets'),
            cell: (token) => token.reference_count.toLocaleString(),
          },
          {
            id: 'marked',
            header: t('Dedicated probe token'),
            cell: (token) => (
              <Switch
                checked={token.marked}
                disabled={
                  markMutation.isPending ||
                  (!token.available && !token.marked) ||
                  (token.marked && token.reference_count > 0)
                }
                aria-label={t('Dedicated probe token')}
                onCheckedChange={(marked) =>
                  markMutation.mutate({ id: token.id, marked })
                }
              />
            ),
          },
        ]}
      />
      <p className='text-muted-foreground mt-3 text-xs'>
        {t(
          'Create and edit token quota, expiry, groups, and model restrictions on the token page.'
        )}
      </p>
    </Dialog>
  )
}

function TargetDialog(props: {
  open: boolean
  target: ModelHealthTarget | null
  targets: ModelHealthTarget[]
  options?: ModelHealthAdminOptions
  optionsLoading: boolean
  optionsError: boolean
  defaultIntervalSeconds: number
  defaultTimeoutSeconds: number
  multiSampleEnabled: boolean
  defaultSamplingMode: ModelHealthSamplingMode
  defaultSamplesPerRun: number
  defaultMinimumSuccesses: number
  defaultSampleSpacingSeconds: number
  saving: boolean
  validating: boolean
  validationResult?: ModelHealthTargetValidationResult
  onOpenChange: (open: boolean) => void
  onManageProbeTokens: () => void
  onSave: (payload: ModelHealthTargetPayload) => void
  onValidate: (payload: ModelHealthTargetPayload) => void
}) {
  const { t } = useTranslation()
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const canKeepCredentials =
    props.target?.mode === 'upstream' &&
    props.target.has_key &&
    !props.target.credentials_need_replacement
  const targetSchema = useMemo(
    () =>
      createTargetSchema(
        t,
        Boolean(canKeepCredentials),
        props.targets,
        props.target?.id
      ),
    [canKeepCredentials, props.target?.id, props.targets, t]
  )
  const defaultTarget = useMemo(
    () =>
      emptyTarget(
        props.defaultIntervalSeconds,
        props.defaultTimeoutSeconds,
        props.defaultSamplingMode,
        props.defaultSamplesPerRun,
        props.defaultMinimumSuccesses,
        props.defaultSampleSpacingSeconds
      ),
    [
      props.defaultIntervalSeconds,
      props.defaultMinimumSuccesses,
      props.defaultSampleSpacingSeconds,
      props.defaultSamplesPerRun,
      props.defaultSamplingMode,
      props.defaultTimeoutSeconds,
    ]
  )
  const form = useForm<TargetFormInput, unknown, TargetFormValues>({
    resolver: zodResolver(targetSchema),
    defaultValues: defaultTarget,
  })
  const models = useFieldArray({ control: form.control, name: 'models' })
  const mode = form.watch('mode')
  const tokenID = form.watch('token_id')
  const observedGroups = form.watch('observed_groups')
  const observedChannelIDs = form.watch('observed_channel_ids')
  const selectedModels = form.watch('models')
  const publishPoolDetail = form.watch('publish_pool_detail')
  const publicGroupAlias = form.watch('public_group_alias')
  const samplingMode = form.watch('sampling_mode')
  const samplesPerRun = form.watch('samples_per_run')
  const intervalSeconds = form.watch('interval_seconds')
  const lastAutomaticAlias = useRef('')
  const lastAutomaticName = useRef('')

  const groupOptions = useMemo(
    () => props.options?.groups ?? [],
    [props.options?.groups]
  )
  const groupSelectableModels = useMemo(
    () => selectableModelsForGroups(groupOptions, observedGroups),
    [groupOptions, observedGroups]
  )
  const selectedToken = useMemo(
    () => props.options?.tokens.find((token) => String(token.id) === tokenID),
    [props.options?.tokens, tokenID]
  )
  const selectableModels = useMemo(() => {
    if (mode !== 'local') return groupSelectableModels
    return modelsAllowedByProbeToken(groupSelectableModels, selectedToken)
  }, [groupSelectableModels, mode, selectedToken])
  const modelOptions = useMemo(
    () => selectableModels.map((model) => ({ value: model, label: model })),
    [selectableModels]
  )
  const tokenOptions = useMemo(() => {
    const options = (props.options?.tokens ?? []).map((token) => ({
      value: String(token.id),
      label: formatProbeTokenLabel(token, t),
    }))
    if (
      props.target?.token_id &&
      !options.some((option) => option.value === String(props.target?.token_id))
    ) {
      options.unshift({
        value: String(props.target.token_id),
        label: t('Token #{{id}} (unavailable)', {
          id: props.target.token_id,
        }),
      })
    }
    return options
  }, [props.options?.tokens, props.target?.token_id, t])
  const selectedModelNames = useMemo(
    () => selectedModels.map((model) => model.name),
    [selectedModels]
  )
  const channelOptions = useMemo(() => {
    const allChannels = props.options?.channels ?? []
    const scopedChannels = selectableChannelsForScope(
      allChannels,
      observedGroups,
      selectedModelNames
    )
    const selectedIDs = new Set(observedChannelIDs)
    const merged = new Map(
      scopedChannels.map((channel) => [channel.id, channel] as const)
    )
    for (const channel of allChannels) {
      if (selectedIDs.has(String(channel.id))) merged.set(channel.id, channel)
    }
    const options = [...merged.values()].map((channel) => ({
      value: String(channel.id),
      label: modelHealthChannelLabel(channel, t('unavailable')),
    }))
    for (const id of observedChannelIDs) {
      if (options.some((option) => option.value === id)) continue
      options.push({
        value: id,
        label: t('Channel #{{id}} (unavailable)', { id }),
      })
    }
    return options
  }, [
    observedChannelIDs,
    observedGroups,
    props.options?.channels,
    selectedModelNames,
    t,
  ])
  const unavailableChannelCount = useMemo(() => {
    const channelsByID = new Map(
      (props.options?.channels ?? []).map((channel) => [
        String(channel.id),
        channel,
      ])
    )
    return observedChannelIDs.filter((id) => {
      const channel = channelsByID.get(id)
      return !channel || channel.available === false || channel.status !== 1
    }).length
  }, [observedChannelIDs, props.options?.channels])
  const unknownModels = useMemo(() => {
    const known = new Set(selectableModels)
    return selectedModelNames.filter((model) => !known.has(model))
  }, [selectableModels, selectedModelNames])
  const estimatedDailyMaximum = useMemo(() => {
    const interval = Math.max(60, Number(intervalSeconds) || 60)
    const runsPerDay = Math.ceil(86_400 / interval)
    const modelCount = Math.max(1, selectedModels.length)
    return runsPerDay * Math.max(1, Number(samplesPerRun) || 1) * modelCount
  }, [intervalSeconds, samplesPerRun, selectedModels.length])

  const automaticAlias = useMemo(
    () => automaticGroupAlias(groupOptions, observedGroups),
    [groupOptions, observedGroups]
  )

  useEffect(() => {
    if (!props.open) return
    setAdvancedOpen(false)
    const initial = props.target ? targetToForm(props.target) : defaultTarget
    form.reset(initial)
    lastAutomaticAlias.current = props.target ? '' : initial.public_group_alias
    lastAutomaticName.current = props.target ? '' : initial.name
  }, [defaultTarget, form, props.open, props.target])

  useEffect(() => {
    if (!props.open) return
    const sampleCount = Number(samplesPerRun)
    const minimum = Number(form.getValues('minimum_successes'))
    if (sampleCount === 1 && minimum !== 1) {
      form.setValue('minimum_successes', 1, {
        shouldDirty: true,
        shouldValidate: true,
      })
    } else if (sampleCount > 1 && minimum > sampleCount) {
      form.setValue('minimum_successes', sampleCount, {
        shouldDirty: true,
        shouldValidate: true,
      })
    }
  }, [form, props.open, samplesPerRun])

  useEffect(() => {
    if (!props.open || props.target) return
    const current = form.getValues('public_group_alias')
    if (!current || current === lastAutomaticAlias.current) {
      form.setValue('public_group_alias', automaticAlias, {
        shouldDirty: Boolean(automaticAlias),
      })
      lastAutomaticAlias.current = automaticAlias
    }
  }, [automaticAlias, form, props.open, props.target])

  useEffect(() => {
    if (!props.open || props.target) return
    const nextName = automaticTargetName(
      mode,
      publicGroupAlias,
      selectedModelNames
    )
    const current = form.getValues('name')
    if (!current || current === lastAutomaticName.current) {
      form.setValue('name', nextName, { shouldDirty: true })
      lastAutomaticName.current = nextName
    }
  }, [
    form,
    mode,
    props.open,
    props.target,
    publicGroupAlias,
    selectedModelNames,
  ])

  useEffect(() => {
    if (
      !props.open ||
      props.target ||
      mode !== 'local' ||
      props.optionsLoading ||
      !props.options
    ) {
      return
    }
    const available = new Set(selectableModels)
    const nextModels = selectedModels.filter((model) =>
      available.has(model.name)
    )
    if (nextModels.length !== selectedModels.length) {
      models.replace(nextModels)
    }
  }, [
    mode,
    models,
    props.open,
    props.options,
    props.optionsLoading,
    props.target,
    selectableModels,
    selectedModels,
  ])

  const submit = (values: TargetFormValues) =>
    props.onSave(formToPayload(values))
  const validate = form.handleSubmit((values) =>
    props.onValidate({ ...formToPayload(values), id: props.target?.id })
  )
  const modelSelectionError =
    form.formState.errors.models?.message ??
    form.formState.errors.models?.root?.message
  let validationBadgeVariant: 'default' | 'secondary' | 'destructive' =
    'default'
  let validationStatusLabel = t('Healthy')
  if (props.validationResult?.batch_status === 'unstable') {
    validationBadgeVariant = 'destructive'
    validationStatusLabel = t('Unstable')
  } else if (props.validationResult?.batch_status === 'fluctuating') {
    validationBadgeVariant = 'secondary'
    validationStatusLabel = t('Fluctuating')
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={props.target ? t('Edit probe target') : t('Add probe target')}
      description={t(
        'Credentials are stored encrypted and are never returned by the API.'
      )}
      contentHeight='min(70vh, 52rem)'
      contentClassName='sm:max-w-4xl'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button
            variant='secondary'
            disabled={props.validating || props.saving}
            onClick={validate}
          >
            <ShieldCheck className='size-4' />
            {t('Validate')}
          </Button>
          <Button
            type='submit'
            form='model-health-target-form'
            disabled={props.saving || props.validating}
          >
            {props.saving ? t('Saving...') : t('Save')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id='model-health-target-form'
          className='space-y-5'
          onSubmit={form.handleSubmit(submit)}
        >
          {props.validationResult && (
            <section className='space-y-3 rounded-lg border p-4'>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <h3 className='font-semibold'>{t('Validation result')}</h3>
                <Badge variant={validationBadgeVariant}>
                  {validationStatusLabel}
                </Badge>
              </div>
              <dl className='grid grid-cols-2 gap-3 text-sm sm:grid-cols-4'>
                <div>
                  <dt className='text-muted-foreground text-xs'>
                    {t('Availability')}
                  </dt>
                  <dd className='mt-1 font-mono font-semibold'>
                    {props.validationResult.availability.toFixed(2)}%
                  </dd>
                </div>
                <div>
                  <dt className='text-muted-foreground text-xs'>
                    {t('Successful attempts')}
                  </dt>
                  <dd className='mt-1 font-mono font-semibold'>
                    {props.validationResult.success_count.toLocaleString()}
                  </dd>
                </div>
                <div>
                  <dt className='text-muted-foreground text-xs'>
                    {t('Actual attempts')}
                  </dt>
                  <dd className='mt-1 font-mono font-semibold'>
                    {props.validationResult.actual_attempts.toLocaleString()}
                  </dd>
                </div>
                <div>
                  <dt className='text-muted-foreground text-xs'>
                    {t('Planned attempts')}
                  </dt>
                  <dd className='mt-1 font-mono font-semibold'>
                    {props.validationResult.planned_attempts.toLocaleString()}
                  </dd>
                </div>
              </dl>
              <div className='overflow-x-auto rounded-md border'>
                <table className='w-full min-w-[34rem] text-left text-xs'>
                  <thead className='bg-muted/50 text-muted-foreground'>
                    <tr>
                      <th className='px-3 py-2 font-medium'>{t('Attempt')}</th>
                      <th className='px-3 py-2 font-medium'>{t('Status')}</th>
                      <th className='px-3 py-2 font-medium'>{t('Latency')}</th>
                      <th className='px-3 py-2 font-medium'>HTTP</th>
                      <th className='px-3 py-2 font-medium'>
                        {t('Error class')}
                      </th>
                    </tr>
                  </thead>
                  <tbody className='divide-y'>
                    {props.validationResult.attempts.map((attempt) => (
                      <tr key={attempt.attempt_index}>
                        <td className='px-3 py-2 font-mono'>
                          #{attempt.attempt_index}
                        </td>
                        <td className='px-3 py-2 font-mono'>
                          {attempt.status}
                        </td>
                        <td className='px-3 py-2 font-mono'>
                          {attempt.latency_ms.toLocaleString()} ms
                        </td>
                        <td className='px-3 py-2 font-mono'>
                          {attempt.http_status || '-'}
                        </td>
                        <td className='px-3 py-2 font-mono'>
                          {attempt.error_class || '-'}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          )}
          <section className='space-y-3 rounded-lg border p-4'>
            <div>
              <h3 className='font-semibold'>{t('Probe source')}</h3>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t(
                  'Choose a dedicated local token or connect directly to an upstream endpoint.'
                )}
              </p>
            </div>

            {props.optionsError && (
              <p className='text-destructive text-sm'>{t('Failed to load')}</p>
            )}

            <div className='grid gap-4 md:grid-cols-2'>
              <FormField
                control={form.control}
                name='mode'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Probe mode')}</FormLabel>
                    <Select
                      items={[
                        { value: 'local', label: t('Local token') },
                        { value: 'upstream', label: t('Upstream endpoint') },
                      ]}
                      value={field.value}
                      onValueChange={field.onChange}
                    >
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          <SelectItem value='local'>
                            {t('Local token')}
                          </SelectItem>
                          <SelectItem value='upstream'>
                            {t('Upstream endpoint')}
                          </SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='protocol'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Protocol')}</FormLabel>
                    <Select
                      items={PROTOCOLS}
                      value={field.value}
                      onValueChange={field.onChange}
                    >
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          {PROTOCOLS.map((protocol) => (
                            <SelectItem
                              key={protocol.value}
                              value={protocol.value}
                            >
                              {protocol.label}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>

            {mode === 'local' ? (
              <FormField
                control={form.control}
                name='token_id'
                render={({ field }) => (
                  <FormItem>
                    <div className='flex flex-wrap items-center justify-between gap-2'>
                      <FormLabel>{t('Dedicated probe token')}</FormLabel>
                      <Button
                        type='button'
                        variant='ghost'
                        size='sm'
                        onClick={props.onManageProbeTokens}
                      >
                        <KeyRound className='size-4' />
                        {t('Manage probe tokens')}
                      </Button>
                    </div>
                    <FormControl>
                      <Combobox
                        options={tokenOptions}
                        value={field.value}
                        onValueChange={(value) => field.onChange(value ?? '')}
                        placeholder={
                          props.optionsLoading
                            ? t('Loading...')
                            : t('Select a dedicated probe token')
                        }
                        emptyText={t('No dedicated probe tokens')}
                        openOnFocus
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Local probes are billed normally and marked as health probes.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            ) : (
              <div className='grid gap-4 md:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='endpoint'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Upstream HTTPS endpoint')}</FormLabel>
                      <FormControl>
                        <Input
                          placeholder={
                            props.target?.endpoint_masked ??
                            'https://api.example.com'
                          }
                          {...field}
                        />
                      </FormControl>
                      <FormDescription>
                        {props.target?.endpoint_masked
                          ? t('Leave blank to keep the saved endpoint.')
                          : t('Only public HTTPS endpoints are accepted.')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='api_key'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Upstream API key')}</FormLabel>
                      <FormControl>
                        <Input
                          type='password'
                          autoComplete='new-password'
                          {...field}
                        />
                      </FormControl>
                      <FormDescription>
                        {props.target?.has_key
                          ? t('Leave blank to keep the saved key.')
                          : t('The key is encrypted before storage.')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
            )}
          </section>

          <section className='space-y-4 rounded-lg border p-4'>
            <div>
              <h3 className='font-semibold'>
                {t('Detection scope and public display')}
              </h3>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t(
                  'Choose one or more observed groups, then select the models to probe.'
                )}
              </p>
            </div>

            <FormField
              control={form.control}
              name='observed_groups'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Observed traffic groups')}</FormLabel>
                  <FormControl>
                    <MultiSelect
                      options={groupOptions.map((group) => ({
                        value: group.name,
                        label: modelHealthGroupLabel(group),
                      }))}
                      selected={field.value}
                      onChange={(values) => field.onChange(values.slice(0, 20))}
                      placeholder={t('Select observed traffic groups')}
                      emptyText={t('No groups available')}
                      maxVisibleChips={6}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Leave empty to show active probe results without observed traffic evidence.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormItem>
              <FormLabel>{t('Models')}</FormLabel>
              <MultiSelect
                options={modelOptions}
                selected={selectedModelNames}
                onChange={(values) => {
                  if (values.length > 20) {
                    toast.error(t('A target supports up to 20 models'))
                    return
                  }
                  models.replace(syncTargetModels(selectedModels, values))
                }}
                placeholder={t('Select models')}
                emptyText={t('No models available for the selected groups')}
                allowCreate={mode === 'upstream'}
                maxVisibleChips={8}
              />
              <FormDescription>
                {observedGroups.length > 1
                  ? t('Only models shared by all selected groups are listed.')
                  : t('Required models determine the target rollup status.')}
              </FormDescription>
              {props.target && unknownModels.length > 0 && (
                <p className='text-xs text-amber-600 dark:text-amber-400'>
                  {t(
                    '{{count}} saved models are not present in the current group options and are preserved.',
                    { count: unknownModels.length }
                  )}
                </p>
              )}
              {modelSelectionError && (
                <p className='text-destructive text-xs font-medium'>
                  {modelSelectionError}
                </p>
              )}
            </FormItem>

            {models.fields.length > 0 && (
              <div className='space-y-2'>
                {models.fields.map((model, index) => (
                  <div
                    key={model.id}
                    className='bg-muted/40 grid items-center gap-3 rounded-lg p-3 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]'
                  >
                    <div className='min-w-0'>
                      <p className='text-muted-foreground text-xs'>
                        {t('Internal model name')}
                      </p>
                      <p
                        className='truncate font-mono text-sm'
                        title={model.name}
                      >
                        {model.name}
                      </p>
                    </div>
                    <FormField
                      control={form.control}
                      name={`models.${index}.public_alias`}
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Public model alias')}</FormLabel>
                          <FormControl>
                            <Input {...field} />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={form.control}
                      name={`models.${index}.required`}
                      render={({ field }) => (
                        <SettingsSwitchItem className='min-w-32'>
                          <SettingsSwitchContent>
                            <FormLabel>{t('Required')}</FormLabel>
                          </SettingsSwitchContent>
                          <FormControl>
                            <Switch
                              checked={field.value}
                              onCheckedChange={field.onChange}
                            />
                          </FormControl>
                        </SettingsSwitchItem>
                      )}
                    />
                  </div>
                ))}
              </div>
            )}

            <FormField
              control={form.control}
              name='public_group_alias'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Public group alias')}</FormLabel>
                  <FormControl>
                    <Input {...field} />
                  </FormControl>
                  <FormDescription>
                    {t('Generated from selected groups and can be edited.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='publish_pool_detail'
              render={({ field }) => (
                <SettingsSwitchItem className='rounded-lg border'>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Publish pool details')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Show this target as an independent pool beneath its group and model summary.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />

            {publishPoolDetail && (
              <div className='grid gap-4 rounded-lg border p-4 md:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='public_pool_alias'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Public pool name')}</FormLabel>
                      <FormControl>
                        <Input {...field} />
                      </FormControl>
                      <FormDescription>
                        {t('This name must be unique within the public group.')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='observed_channel_ids'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Observed traffic channels')}</FormLabel>
                      <FormControl>
                        <MultiSelect
                          options={channelOptions}
                          selected={field.value}
                          onChange={(values) =>
                            field.onChange(values.slice(0, 20))
                          }
                          placeholder={t('Select channels for this pool')}
                          emptyText={t(
                            'No channels match the selected groups and models'
                          )}
                          maxVisibleChips={6}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Pool traffic metrics include only actual upstream attempts on these channels.'
                        )}
                      </FormDescription>
                      {unavailableChannelCount > 0 && (
                        <p className='text-xs text-amber-600 dark:text-amber-400'>
                          {t(
                            '{{count}} saved channels are unavailable and are preserved.',
                            { count: unavailableChannelCount }
                          )}
                        </p>
                      )}
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
            )}

            <SettingsControlGroup className='grid gap-0 md:grid-cols-2'>
              <FormField
                control={form.control}
                name='enabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Enabled')}</FormLabel>
                      <FormDescription>
                        {t('Include this target in scheduled probes.')}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </SettingsSwitchItem>
                )}
              />
              <FormField
                control={form.control}
                name='public'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Public')}</FormLabel>
                      <FormDescription>
                        {t(
                          'Allow its aliases and aggregate metrics on the health page.'
                        )}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </SettingsSwitchItem>
                )}
              />
            </SettingsControlGroup>
          </section>

          <Collapsible
            open={advancedOpen}
            onOpenChange={setAdvancedOpen}
            className='rounded-lg border'
          >
            <CollapsibleTrigger
              render={
                <Button
                  type='button'
                  variant='ghost'
                  className='group w-full justify-between rounded-lg px-4'
                  aria-expanded={advancedOpen}
                />
              }
            >
              {t('Advanced Settings')}
              <ChevronDown
                className={`size-4 transition-transform ${advancedOpen ? 'rotate-180' : ''}`}
                aria-hidden='true'
              />
            </CollapsibleTrigger>
            <CollapsibleContent className='space-y-4 border-t p-4'>
              <FormField
                control={form.control}
                name='name'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Internal name')}</FormLabel>
                    <FormControl>
                      <Input {...field} />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Generated automatically and only visible to administrators.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <div className='grid gap-4 md:grid-cols-3'>
                <FormField
                  control={form.control}
                  name='interval_seconds'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Interval (seconds)')}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={60}
                          max={3600}
                          {...safeNumberFieldProps(field)}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='timeout_seconds'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Timeout (seconds)')}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={1}
                          max={300}
                          {...safeNumberFieldProps(field)}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='latency_slo_ms'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Latency SLO (ms)')}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={1}
                          placeholder={t('Optional')}
                          {...field}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              <div className='space-y-3 rounded-lg border p-4'>
                <div>
                  <h4 className='text-sm font-semibold'>
                    {t('Sampling within each run')}
                  </h4>
                  <p className='text-muted-foreground mt-1 text-xs'>
                    {props.multiSampleEnabled
                      ? t(
                          'Each model is sampled sequentially while different models may still run in parallel.'
                        )
                      : t(
                          'Multi-sample runs are globally disabled; this configuration takes effect after the global switch is enabled.'
                        )}
                  </p>
                </div>
                <FormField
                  control={form.control}
                  name='sampling_mode'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Sampling mode')}</FormLabel>
                      <FormControl>
                        <div
                          role='radiogroup'
                          aria-label={t('Sampling mode')}
                          className='bg-muted grid grid-cols-2 gap-1 rounded-md p-1'
                        >
                          <Button
                            type='button'
                            role='radio'
                            aria-checked={field.value === 'fixed'}
                            variant={
                              field.value === 'fixed' ? 'secondary' : 'ghost'
                            }
                            size='sm'
                            className='min-w-0'
                            onClick={() => field.onChange('fixed')}
                          >
                            {t('Fixed samples')}
                          </Button>
                          <Button
                            type='button'
                            role='radio'
                            aria-checked={field.value === 'confirm_on_failure'}
                            variant={
                              field.value === 'confirm_on_failure'
                                ? 'secondary'
                                : 'ghost'
                            }
                            size='sm'
                            className='min-w-0'
                            onClick={() => field.onChange('confirm_on_failure')}
                          >
                            {t('Confirm on failure')}
                          </Button>
                        </div>
                      </FormControl>
                      <FormDescription>
                        {samplingMode === 'fixed'
                          ? t('Every run always performs {{count}} attempts.', {
                              count: Number(samplesPerRun) || 1,
                            })
                          : t(
                              'A successful first attempt ends the run; a failure triggers up to {{count}} attempts.',
                              { count: Number(samplesPerRun) || 1 }
                            )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <div className='grid gap-4 md:grid-cols-3'>
                  <FormField
                    control={form.control}
                    name='samples_per_run'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Samples per run')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={1}
                            max={5}
                            {...safeNumberFieldProps(field)}
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name='minimum_successes'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Minimum successes')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={1}
                            max={Math.max(1, Number(samplesPerRun) || 1)}
                            disabled={Number(samplesPerRun) <= 1}
                            {...safeNumberFieldProps(field)}
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name='sample_spacing_seconds'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Attempt spacing (seconds)')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={1}
                            max={30}
                            disabled={Number(samplesPerRun) <= 1}
                            {...safeNumberFieldProps(field)}
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>
                <div className='bg-muted/40 grid gap-2 rounded-md p-3 text-xs sm:grid-cols-2'>
                  <p>
                    <span className='text-muted-foreground'>
                      {t('Cost multiplier')}:{' '}
                    </span>
                    <strong>
                      {samplingMode === 'fixed'
                        ? t('Fixed {{count}}x', {
                            count: Number(samplesPerRun) || 1,
                          })
                        : t('About 1x normally, up to {{count}}x on failure', {
                            count: Number(samplesPerRun) || 1,
                          })}
                    </strong>
                  </p>
                  <p>
                    <span className='text-muted-foreground'>
                      {t('Maximum attempts per day')}:{' '}
                    </span>
                    <strong>{estimatedDailyMaximum.toLocaleString()}</strong>
                  </p>
                </div>
              </div>
            </CollapsibleContent>
          </Collapsible>
        </form>
      </Form>
    </Dialog>
  )
}
