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
import { ChevronDown } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
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
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'
import { ModelHealthTargets } from './model-health-targets'

export type ModelHealthSettingsDefaults = {
  'model_health_setting.enabled': boolean
  'model_health_setting.pool_details_enabled': boolean
  'model_health_setting.multi_sample_enabled': boolean
  'model_health_setting.default_interval_seconds': number
  'model_health_setting.default_timeout_seconds': number
  'model_health_setting.default_sampling_mode': 'fixed' | 'confirm_on_failure'
  'model_health_setting.default_samples_per_run': number
  'model_health_setting.default_minimum_successes': number
  'model_health_setting.default_sample_spacing_seconds': number
  'model_health_setting.concurrency': number
  'model_health_setting.retention_days': number
  'model_health_setting.healthy_threshold': number
  'model_health_setting.fluctuating_threshold': number
  'model_health_setting.passive_min_samples': number
  'model_health_setting.active_min_samples': number
  'model_health_setting.public_groups': string
  'model_health_setting.public_models': string
  'perf_metrics_setting.enabled': boolean
  'perf_metrics_setting.flush_interval': number
  'perf_metrics_setting.bucket_time': 'minute' | '5min' | 'hour'
  'perf_metrics_setting.retention_days': number
}

const createSettingsSchema = (t: (key: string) => string) => {
  return z
    .object({
      enabled: z.boolean(),
      pool_details_enabled: z.boolean(),
      multi_sample_enabled: z.boolean(),
      default_interval_seconds: z.coerce.number().int().min(60).max(3600),
      default_timeout_seconds: z.coerce.number().int().min(1).max(300),
      default_sampling_mode: z.enum(['fixed', 'confirm_on_failure']),
      default_samples_per_run: z.coerce.number().int().min(1).max(5),
      default_minimum_successes: z.coerce.number().int().min(1).max(5),
      default_sample_spacing_seconds: z.coerce.number().int().min(1).max(30),
      concurrency: z.coerce.number().int().min(1).max(32),
      retention_days: z.coerce.number().int().min(1).max(365),
      healthy_threshold: z.coerce.number().min(0.1).max(100),
      fluctuating_threshold: z.coerce.number().min(0.1).max(100),
      passive_min_samples: z.coerce.number().int().min(1),
      active_min_samples: z.coerce.number().int().min(1),
      perf_enabled: z.boolean(),
      perf_flush_interval: z.coerce.number().int().min(1),
      perf_bucket_time: z.enum(['minute', '5min', 'hour']),
      perf_retention_days: z.coerce.number().int().min(0),
    })
    .superRefine((value, context) => {
      if (value.healthy_threshold < value.fluctuating_threshold) {
        context.addIssue({
          code: 'custom',
          path: ['healthy_threshold'],
          message: t(
            'Healthy threshold must not be lower than fluctuating threshold'
          ),
        })
      }
      if (value.default_minimum_successes > value.default_samples_per_run) {
        context.addIssue({
          code: 'custom',
          path: ['default_minimum_successes'],
          message: t('Minimum successes cannot exceed samples per run'),
        })
      }
      const maximumRunSeconds =
        value.default_samples_per_run * value.default_timeout_seconds +
        (value.default_samples_per_run - 1) *
          value.default_sample_spacing_seconds
      if (maximumRunSeconds > value.default_interval_seconds) {
        context.addIssue({
          code: 'custom',
          path: ['default_samples_per_run'],
          message: t(
            'The maximum sampling duration must fit within the probe interval'
          ),
        })
      }
    })
}

type SettingsFormInput = z.input<ReturnType<typeof createSettingsSchema>>
type SettingsFormValues = z.output<ReturnType<typeof createSettingsSchema>>

function buildDefaults(
  defaults: ModelHealthSettingsDefaults
): SettingsFormInput {
  return {
    enabled: defaults['model_health_setting.enabled'],
    pool_details_enabled: defaults['model_health_setting.pool_details_enabled'],
    multi_sample_enabled: defaults['model_health_setting.multi_sample_enabled'],
    default_interval_seconds:
      defaults['model_health_setting.default_interval_seconds'],
    default_timeout_seconds:
      defaults['model_health_setting.default_timeout_seconds'],
    default_sampling_mode:
      defaults['model_health_setting.default_sampling_mode'],
    default_samples_per_run:
      defaults['model_health_setting.default_samples_per_run'],
    default_minimum_successes:
      defaults['model_health_setting.default_minimum_successes'],
    default_sample_spacing_seconds:
      defaults['model_health_setting.default_sample_spacing_seconds'],
    concurrency: defaults['model_health_setting.concurrency'],
    retention_days: defaults['model_health_setting.retention_days'],
    healthy_threshold: defaults['model_health_setting.healthy_threshold'],
    fluctuating_threshold:
      defaults['model_health_setting.fluctuating_threshold'],
    passive_min_samples: defaults['model_health_setting.passive_min_samples'],
    active_min_samples: defaults['model_health_setting.active_min_samples'],
    perf_enabled: defaults['perf_metrics_setting.enabled'],
    perf_flush_interval: defaults['perf_metrics_setting.flush_interval'],
    perf_bucket_time: defaults['perf_metrics_setting.bucket_time'],
    perf_retention_days: defaults['perf_metrics_setting.retention_days'],
  }
}

export function ModelHealthSection(props: {
  defaultValues: ModelHealthSettingsDefaults
}) {
  const { t } = useTranslation()
  const settingsSchema = useMemo(() => createSettingsSchema(t), [t])
  const updateOption = useUpdateOption()
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const formDefaults = useMemo(
    () => buildDefaults(props.defaultValues),
    [props.defaultValues]
  )
  const form = useForm<SettingsFormInput, unknown, SettingsFormValues>({
    resolver: zodResolver(settingsSchema),
    defaultValues: formDefaults,
  })
  useResetForm(form, formDefaults)
  const perfEnabled = form.watch('perf_enabled')
  const defaultSamplesPerRun = form.watch('default_samples_per_run')

  const onSubmit = async (values: SettingsFormValues) => {
    const updates: Array<{
      field: keyof SettingsFormValues
      key: string
      value: string | number | boolean
    }> = [
      {
        field: 'enabled',
        key: 'model_health_setting.enabled',
        value: values.enabled,
      },
      {
        field: 'pool_details_enabled',
        key: 'model_health_setting.pool_details_enabled',
        value: values.pool_details_enabled,
      },
      {
        field: 'multi_sample_enabled',
        key: 'model_health_setting.multi_sample_enabled',
        value: values.multi_sample_enabled,
      },
      {
        field: 'default_interval_seconds',
        key: 'model_health_setting.default_interval_seconds',
        value: values.default_interval_seconds,
      },
      {
        field: 'default_timeout_seconds',
        key: 'model_health_setting.default_timeout_seconds',
        value: values.default_timeout_seconds,
      },
      {
        field: 'default_sampling_mode',
        key: 'model_health_setting.default_sampling_mode',
        value: values.default_sampling_mode,
      },
      {
        field: 'default_samples_per_run',
        key: 'model_health_setting.default_samples_per_run',
        value: values.default_samples_per_run,
      },
      {
        field: 'default_minimum_successes',
        key: 'model_health_setting.default_minimum_successes',
        value: values.default_minimum_successes,
      },
      {
        field: 'default_sample_spacing_seconds',
        key: 'model_health_setting.default_sample_spacing_seconds',
        value: values.default_sample_spacing_seconds,
      },
      {
        field: 'concurrency',
        key: 'model_health_setting.concurrency',
        value: values.concurrency,
      },
      {
        field: 'retention_days',
        key: 'model_health_setting.retention_days',
        value: values.retention_days,
      },
      {
        field: 'healthy_threshold',
        key: 'model_health_setting.healthy_threshold',
        value: values.healthy_threshold,
      },
      {
        field: 'fluctuating_threshold',
        key: 'model_health_setting.fluctuating_threshold',
        value: values.fluctuating_threshold,
      },
      {
        field: 'passive_min_samples',
        key: 'model_health_setting.passive_min_samples',
        value: values.passive_min_samples,
      },
      {
        field: 'active_min_samples',
        key: 'model_health_setting.active_min_samples',
        value: values.active_min_samples,
      },
      {
        field: 'perf_enabled',
        key: 'perf_metrics_setting.enabled',
        value: values.perf_enabled,
      },
      {
        field: 'perf_flush_interval',
        key: 'perf_metrics_setting.flush_interval',
        value: values.perf_flush_interval,
      },
      {
        field: 'perf_bucket_time',
        key: 'perf_metrics_setting.bucket_time',
        value: values.perf_bucket_time,
      },
      {
        field: 'perf_retention_days',
        key: 'perf_metrics_setting.retention_days',
        value: values.perf_retention_days,
      },
    ]

    const changed = updates.filter(
      (update) => form.formState.dirtyFields[update.field]
    )
    if (changed.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    for (const update of changed) {
      const result = await updateOption.mutateAsync({
        key: update.key,
        value: update.value,
      })
      if (!result.success) return
    }
    form.reset(values)
    toast.success(t('Model health settings saved'))
  }

  return (
    <div className='space-y-4'>
      <SettingsSection title={t('Model health')}>
        <Form {...form}>
          <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
            <SettingsPageFormActions
              onSave={form.handleSubmit(onSubmit)}
              isSaving={updateOption.isPending}
            />

            <FormField
              control={form.control}
              name='enabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Enable scheduled health probes')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Targets remain saved while scheduled collection is paused.'
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

            <FormField
              control={form.control}
              name='pool_details_enabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Enable public pool details')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Channel metrics continue collecting while public pool details are hidden.'
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

            <FormField
              control={form.control}
              name='multi_sample_enabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Enable multi-sample probe runs')}</FormLabel>
                    <FormDescription>
                      {t(
                        'When disabled, every target runs one probe per model regardless of its sampling settings.'
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

            <div>
              <h3 className='font-semibold'>{t('Probe defaults')}</h3>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t(
                  'New targets use these scheduling and sampling values by default.'
                )}
              </p>
            </div>
            <div className='grid gap-4 md:grid-cols-2'>
              <NumberField
                form={form}
                name='default_interval_seconds'
                label={t('Interval (seconds)')}
                min={60}
                max={3600}
              />
              <NumberField
                form={form}
                name='default_timeout_seconds'
                label={t('Timeout (seconds)')}
                min={1}
                max={300}
              />
            </div>
            <div className='grid gap-4 md:grid-cols-2 xl:grid-cols-4'>
              <FormField
                control={form.control}
                name='default_sampling_mode'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Default sampling mode')}</FormLabel>
                    <Select
                      items={[
                        { value: 'fixed', label: t('Fixed samples') },
                        {
                          value: 'confirm_on_failure',
                          label: t('Confirm on failure'),
                        },
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
                          <SelectItem value='fixed'>
                            {t('Fixed samples')}
                          </SelectItem>
                          <SelectItem value='confirm_on_failure'>
                            {t('Confirm on failure')}
                          </SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <NumberField
                form={form}
                name='default_samples_per_run'
                label={t('Samples per run')}
                min={1}
                max={5}
                onValueChange={(value) => {
                  const minimum = Number(
                    form.getValues('default_minimum_successes')
                  )
                  if (value === 1 || minimum > value) {
                    form.setValue(
                      'default_minimum_successes',
                      value === 1 ? 1 : value,
                      { shouldDirty: true, shouldValidate: true }
                    )
                  }
                }}
              />
              <NumberField
                form={form}
                name='default_minimum_successes'
                label={t('Minimum successes')}
                min={1}
                max={Math.max(1, Number(defaultSamplesPerRun) || 1)}
                disabled={Number(defaultSamplesPerRun) <= 1}
              />
              <NumberField
                form={form}
                name='default_sample_spacing_seconds'
                label={t('Attempt spacing (seconds)')}
                min={1}
                max={30}
                disabled={Number(defaultSamplesPerRun) <= 1}
              />
            </div>

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
              <CollapsibleContent className='space-y-5 border-t p-4'>
                <div>
                  <h3 className='font-semibold'>
                    {t('Scheduling and retention')}
                  </h3>
                </div>
                <div className='grid gap-4 md:grid-cols-2'>
                  <NumberField
                    form={form}
                    name='concurrency'
                    label={t('Concurrent probes')}
                    min={1}
                    max={32}
                  />
                  <NumberField
                    form={form}
                    name='retention_days'
                    label={t('Retention days')}
                    min={1}
                    max={365}
                  />
                </div>

                <div>
                  <h3 className='font-semibold'>{t('Status thresholds')}</h3>
                  <p className='text-muted-foreground mt-1 text-xs'>
                    {t(
                      'Active and observed percentages stay separate and use the same severity thresholds.'
                    )}
                  </p>
                </div>
                <div className='grid gap-4 md:grid-cols-4'>
                  <NumberField
                    form={form}
                    name='healthy_threshold'
                    label={t('Healthy threshold (%)')}
                    min={0.1}
                    max={100}
                    step={0.1}
                  />
                  <NumberField
                    form={form}
                    name='fluctuating_threshold'
                    label={t('Fluctuating threshold (%)')}
                    min={0.1}
                    max={100}
                    step={0.1}
                  />
                  <NumberField
                    form={form}
                    name='active_min_samples'
                    label={t('Active minimum samples')}
                    min={1}
                  />
                  <NumberField
                    form={form}
                    name='passive_min_samples'
                    label={t('Observed minimum requests')}
                    min={1}
                  />
                </div>

                <div>
                  <h3 className='font-semibold'>
                    {t('Observed performance metrics')}
                  </h3>
                  <p className='text-muted-foreground mt-1 text-xs'>
                    {t(
                      'Real relay traffic is aggregated separately from active probes.'
                    )}
                  </p>
                </div>
                <div className='grid gap-4 md:grid-cols-4'>
                  <FormField
                    control={form.control}
                    name='perf_enabled'
                    render={({ field }) => (
                      <SettingsSwitchItem>
                        <SettingsSwitchContent>
                          <FormLabel>
                            {t('Enable model performance metrics')}
                          </FormLabel>
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
                  <NumberField
                    form={form}
                    name='perf_flush_interval'
                    label={t('Flush interval (minutes)')}
                    min={1}
                    disabled={!perfEnabled}
                  />
                  <FormField
                    control={form.control}
                    name='perf_bucket_time'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Aggregation bucket')}</FormLabel>
                        <Select
                          items={[
                            { value: 'minute', label: t('1 minute') },
                            { value: '5min', label: t('5 minutes') },
                            { value: 'hour', label: t('1 hour') },
                          ]}
                          value={field.value}
                          onValueChange={field.onChange}
                          disabled={!perfEnabled}
                        >
                          <FormControl>
                            <SelectTrigger>
                              <SelectValue />
                            </SelectTrigger>
                          </FormControl>
                          <SelectContent alignItemWithTrigger={false}>
                            <SelectGroup>
                              <SelectItem value='minute'>
                                {t('1 minute')}
                              </SelectItem>
                              <SelectItem value='5min'>
                                {t('5 minutes')}
                              </SelectItem>
                              <SelectItem value='hour'>
                                {t('1 hour')}
                              </SelectItem>
                            </SelectGroup>
                          </SelectContent>
                        </Select>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <NumberField
                    form={form}
                    name='perf_retention_days'
                    label={t('Retention days')}
                    min={0}
                    disabled={!perfEnabled}
                    description={t('0 means data is kept permanently')}
                  />
                </div>
              </CollapsibleContent>
            </Collapsible>
          </SettingsForm>
        </Form>
      </SettingsSection>

      <SettingsSection title={t('Probe targets')}>
        <ModelHealthTargets
          defaultIntervalSeconds={Number(
            form.watch('default_interval_seconds')
          )}
          defaultTimeoutSeconds={Number(form.watch('default_timeout_seconds'))}
          multiSampleEnabled={form.watch('multi_sample_enabled')}
          defaultSamplingMode={form.watch('default_sampling_mode')}
          defaultSamplesPerRun={Number(form.watch('default_samples_per_run'))}
          defaultMinimumSuccesses={Number(
            form.watch('default_minimum_successes')
          )}
          defaultSampleSpacingSeconds={Number(
            form.watch('default_sample_spacing_seconds')
          )}
        />
      </SettingsSection>
    </div>
  )
}

function NumberField(props: {
  form: ReturnType<
    typeof useForm<SettingsFormInput, unknown, SettingsFormValues>
  >
  name:
    | 'default_interval_seconds'
    | 'default_timeout_seconds'
    | 'default_samples_per_run'
    | 'default_minimum_successes'
    | 'default_sample_spacing_seconds'
    | 'concurrency'
    | 'retention_days'
    | 'healthy_threshold'
    | 'fluctuating_threshold'
    | 'passive_min_samples'
    | 'active_min_samples'
    | 'perf_flush_interval'
    | 'perf_retention_days'
  label: string
  min: number
  max?: number
  step?: number
  disabled?: boolean
  description?: string
  onValueChange?: (value: number) => void
}) {
  return (
    <FormField
      control={props.form.control}
      name={props.name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{props.label}</FormLabel>
          <FormControl>
            <Input
              type='number'
              min={props.min}
              max={props.max}
              step={props.step ?? 1}
              disabled={props.disabled}
              {...safeNumberFieldProps(field)}
              onChange={(event) => {
                const value = event.target.valueAsNumber
                if (!Number.isFinite(value)) return
                field.onChange(value)
                props.onValueChange?.(value)
              }}
            />
          </FormControl>
          {props.description && (
            <FormDescription>{props.description}</FormDescription>
          )}
          <FormMessage />
        </FormItem>
      )}
    />
  )
}
