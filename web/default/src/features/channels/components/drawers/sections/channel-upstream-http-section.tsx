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
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

import type { ChannelFormValues } from '../../../lib'
import type {
  EffectiveUpstreamHTTPConfig,
  UpstreamHTTPMode,
} from '../../../types'

type ChannelUpstreamHTTPSectionProps = {
  globalConfig: EffectiveUpstreamHTTPConfig
}

function optionalIntegerFieldProps(
  field: {
    value?: number
    onChange: (value: number | undefined) => void
    onBlur: () => void
    name: string
    ref: React.Ref<HTMLInputElement>
  },
  disabled: boolean
) {
  return {
    value: field.value ?? '',
    onChange: (event: React.ChangeEvent<HTMLInputElement>) => {
      if (event.target.value === '') {
        field.onChange(undefined)
        return
      }
      field.onChange(event.target.valueAsNumber)
    },
    onBlur: field.onBlur,
    name: field.name,
    ref: field.ref,
    disabled,
  }
}

export function ChannelUpstreamHTTPSection(
  props: ChannelUpstreamHTTPSectionProps
) {
  const { t } = useTranslation()
  const form = useFormContext<ChannelFormValues>()
  const upstreamHTTPMode = useWatch({
    control: form.control,
    name: 'upstream_http_mode',
  })
  const http2ConnectionPoolSize = useWatch({
    control: form.control,
    name: 'http2_connection_pool_size',
  })
  const http1BodyThresholdKiB = useWatch({
    control: form.control,
    name: 'http1_body_threshold_kib',
  })

  const effectiveMode =
    upstreamHTTPMode ?? props.globalConfig.upstream_http_mode
  const effectivePoolSize =
    http2ConnectionPoolSize ?? props.globalConfig.http2_connection_pool_size
  const effectiveThresholdKiB =
    http1BodyThresholdKiB ?? props.globalConfig.http1_body_threshold_kib
  const transportFieldsDisabled = effectiveMode === 'http1'

  const modeLabel = (mode: UpstreamHTTPMode): string => {
    if (mode === 'http1') return t('HTTP/1.1 only')
    if (mode === 'hybrid') return t('Hybrid by request size')
    return t('Automatic (HTTP/2 preferred)')
  }

  const sourceLabel = (source: string | undefined): string => {
    if (source === 'channel') return t('channel override')
    if (source === 'global') return t('system setting')
    if (source === 'environment') return t('environment variable')
    if (source === 'legacy_force_http1') return t('legacy HTTP/1 setting')
    return t('built-in default')
  }

  return (
    <div className='space-y-3 px-4 py-3'>
      <div>
        <h4 className='text-sm font-medium'>{t('Upstream HTTP transport')}</h4>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t(
            'Choose the protocol strategy for this channel. Requests are never retried across protocols.'
          )}
        </p>
      </div>

      <FormField
        control={form.control}
        name='upstream_http_mode'
        render={({ field }) => {
          const selectedMode = field.value ?? 'inherit'
          return (
            <FormItem>
              <FormLabel>{t('Transport mode')}</FormLabel>
              <FormControl>
                <ToggleGroup
                  value={[selectedMode]}
                  onValueChange={(values) => {
                    const nextMode = values.find(
                      (value) => value !== selectedMode
                    )
                    if (!nextMode) return

                    const nextValue =
                      nextMode === 'inherit'
                        ? undefined
                        : (nextMode as UpstreamHTTPMode)
                    field.onChange(nextValue)
                    form.setValue('force_http1', nextValue === 'http1', {
                      shouldDirty: true,
                    })
                  }}
                  aria-label={t('Transport mode')}
                  variant='outline'
                  size='sm'
                  spacing={0}
                  className='grid w-full grid-cols-2 sm:grid-cols-4'
                >
                  <ToggleGroupItem value='inherit' className='w-full'>
                    {t('Inherit system')}
                  </ToggleGroupItem>
                  <ToggleGroupItem value='auto' className='w-full'>
                    {t('Automatic')}
                  </ToggleGroupItem>
                  <ToggleGroupItem value='hybrid' className='w-full'>
                    {t('Hybrid')}
                  </ToggleGroupItem>
                  <ToggleGroupItem value='http1' className='w-full'>
                    HTTP/1.1
                  </ToggleGroupItem>
                </ToggleGroup>
              </FormControl>
              <FormDescription>
                {t('Current effective mode: {{mode}} ({{source}})', {
                  mode: modeLabel(effectiveMode),
                  source:
                    upstreamHTTPMode === undefined
                      ? sourceLabel(
                          props.globalConfig.upstream_http_mode_source
                        )
                      : t('channel override'),
                })}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )
        }}
      />

      <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
        <FormField
          control={form.control}
          name='http2_connection_pool_size'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('HTTP/2 connection pool size')}</FormLabel>
              <FormControl>
                <Input
                  type='number'
                  min={1}
                  max={64}
                  step={1}
                  placeholder={String(
                    props.globalConfig.http2_connection_pool_size
                  )}
                  {...optionalIntegerFieldProps(field, transportFieldsDisabled)}
                />
              </FormControl>
              <FormDescription>
                {t('Allowed range: 1–64. Leave empty to inherit.')}
                <span className='mt-1 block'>
                  {t('Current effective value: {{value}} ({{source}})', {
                    value: effectivePoolSize,
                    source:
                      http2ConnectionPoolSize === undefined
                        ? sourceLabel(
                            props.globalConfig.http2_connection_pool_size_source
                          )
                        : t('channel override'),
                  })}
                </span>
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />

        <FormField
          control={form.control}
          name='http1_body_threshold_kib'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('HTTP/1 body threshold (KiB)')}</FormLabel>
              <FormControl>
                <Input
                  type='number'
                  min={64}
                  max={65536}
                  step={1}
                  placeholder={String(
                    props.globalConfig.http1_body_threshold_kib
                  )}
                  {...optionalIntegerFieldProps(field, transportFieldsDisabled)}
                />
              </FormControl>
              <FormDescription>
                {t(
                  'Hybrid mode sends bodies at or above this size, and bodies with unknown length, over HTTP/1.1.'
                )}
                <span className='mt-1 block'>
                  {t('Current effective value: {{value}} KiB ({{source}})', {
                    value: effectiveThresholdKiB,
                    source:
                      http1BodyThresholdKiB === undefined
                        ? sourceLabel(
                            props.globalConfig.http1_body_threshold_kib_source
                          )
                        : t('channel override'),
                  })}
                </span>
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </div>

      {effectiveMode === 'http1' ? (
        <Alert>
          <AlertDescription>
            {t(
              'HTTP/1.1 can avoid upstream HTTP/2 congestion, but may create many more TCP and TLS connections.'
            )}
          </AlertDescription>
        </Alert>
      ) : null}
    </div>
  )
}
