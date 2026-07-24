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
import { VChart } from '@visactor/react-vchart'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { VCHART_OPTION } from '@/lib/vchart'

import {
  buildHealthTrendData,
  getHealthTrendYDomain,
  HEALTH_TREND_SIGNALS,
  HEALTH_TREND_SIGNAL_STYLES,
  type HealthTrendSignal,
} from './health-trend-data'
import { buildHealthTrendSpec } from './health-trend-spec'
import type { ModelHealthPoint } from './types'

const HEALTH_VCHART_OPTIONS = {
  ...VCHART_OPTION,
  animation: false,
} as const

export function HealthTrend(props: {
  points: ModelHealthPoint[]
  bucketSeconds: number
  resolvedTheme: string
  themeReady: boolean
  observedLabel?: string
}) {
  const { t } = useTranslation()
  const containerRef = useRef<HTMLDivElement | null>(null)
  const [visible, setVisible] = useState(false)
  const signalLabels = useMemo<Record<HealthTrendSignal, string>>(() => {
    return {
      probe: t('Active probe'),
      observed: props.observedLabel ?? t('Observed traffic'),
    }
  }, [props.observedLabel, t])

  useEffect(() => {
    const element = containerRef.current
    if (!element || visible) return
    if (typeof IntersectionObserver === 'undefined') {
      setVisible(true)
      return
    }
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setVisible(true)
          observer.disconnect()
        }
      },
      { rootMargin: '240px' }
    )
    observer.observe(element)
    return () => observer.disconnect()
  }, [visible])
  const data = useMemo(
    () =>
      buildHealthTrendData(
        props.points,
        signalLabels,
        undefined,
        props.bucketSeconds
      ),
    [props.bucketSeconds, props.points, signalLabels]
  )
  const yDomain = useMemo(() => getHealthTrendYDomain(data), [data])
  const hasData = useMemo(
    () =>
      HEALTH_TREND_SIGNALS.some((signal) =>
        data[signal].some((datum) => datum.value != null)
      ),
    [data]
  )
  const referenceStroke =
    props.resolvedTheme === 'dark'
      ? 'rgba(148, 163, 184, 0.2)'
      : 'rgba(100, 116, 139, 0.16)'
  const endpointStroke = props.resolvedTheme === 'dark' ? '#111827' : '#ffffff'

  const spec = useMemo(
    () =>
      buildHealthTrendSpec({
        data,
        yDomain,
        endpointStroke,
        referenceStroke,
        formatProbeTooltip: (
          successCount,
          attemptCount,
          percentage,
          runCount
        ) => {
          if (runCount != null && runCount > 1) {
            return t(
              '{{runs}} runs - {{success}}/{{attempts}} attempts successful - {{percentage}}% equal-weighted per run',
              {
                runs: runCount,
                success: successCount,
                attempts: attemptCount,
                percentage: percentage.toFixed(2),
              }
            )
          }
          return t('{{success}}/{{attempts}} successful - {{percentage}}%', {
            success: successCount,
            attempts: attemptCount,
            percentage: percentage.toFixed(2),
          })
        },
      }),
    [data, endpointStroke, referenceStroke, t, yDomain]
  )

  return (
    <div ref={containerRef} className='min-w-0 flex-1'>
      <div
        role='list'
        className='text-muted-foreground mb-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] leading-4 sm:justify-end'
      >
        {HEALTH_TREND_SIGNALS.map((signal) => {
          const style = HEALTH_TREND_SIGNAL_STYLES[signal]
          return (
            <span
              key={signal}
              role='listitem'
              className='inline-flex items-center gap-1.5 whitespace-nowrap'
            >
              <svg
                aria-hidden='true'
                className='h-2 w-7 shrink-0 overflow-visible'
                viewBox='0 0 28 8'
              >
                <line
                  x1='1'
                  x2='27'
                  y1='4'
                  y2='4'
                  stroke={style.color}
                  strokeDasharray={style.lineDash.join(' ') || undefined}
                  strokeLinecap='round'
                  strokeWidth='2'
                />
              </svg>
              <span>{signalLabels[signal]}</span>
            </span>
          )
        })}
      </div>
      <div className='h-16 min-w-0'>
        {visible && props.themeReady && hasData ? (
          <VChart
            key={`health-${props.resolvedTheme}`}
            spec={{
              ...spec,
              theme: props.resolvedTheme === 'dark' ? 'dark' : 'light',
              background: 'transparent',
            }}
            options={HEALTH_VCHART_OPTIONS}
          />
        ) : (
          <div className='bg-muted/40 h-16 w-full rounded-md' />
        )}
      </div>
    </div>
  )
}
