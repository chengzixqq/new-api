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
import {
  Activity,
  AlertTriangle,
  ChevronDown,
  CircleDashed,
  Clock3,
  HeartPulse,
  RadioTower,
  RefreshCw,
  Timer,
  Waves,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import { MultiSelect } from '@/components/multi-select'
import { PageTransition } from '@/components/page-transition'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Skeleton } from '@/components/ui/skeleton'
import { toIntlLocale } from '@/i18n/languages'
import { useChartTheme } from '@/lib/use-chart-theme'
import { cn } from '@/lib/utils'

import { HealthTrend } from './health-trend'
import { useModelHealth } from './hooks'
import {
  buildModelHealthCards,
  publicHealthPoolKey,
  publicHealthRowKey,
  type ModelHealthCard,
} from './pool-hierarchy'
import type {
  ModelHealthFilters,
  ModelHealthPoolRow,
  ModelHealthPoolSeriesRow,
  ModelHealthRow,
  ModelHealthSeriesRow,
  ModelHealthStatus,
  ModelHealthSummary,
  ModelHealthWindow,
} from './types'

const WINDOWS: ModelHealthWindow[] = ['12h', '24h', '7d', '30d']
const STATUSES: ModelHealthStatus[] = [
  'unstable',
  'fluctuating',
  'healthy',
  'idle',
]

const STATUS_META: Record<
  ModelHealthStatus,
  { label: string; dot: string; text: string; border: string }
> = {
  healthy: {
    label: 'Healthy',
    dot: 'bg-emerald-500',
    text: 'text-emerald-600 dark:text-emerald-400',
    border: 'border-emerald-500/20',
  },
  fluctuating: {
    label: 'Fluctuating',
    dot: 'bg-amber-500',
    text: 'text-amber-600 dark:text-amber-400',
    border: 'border-amber-500/20',
  },
  unstable: {
    label: 'Unstable',
    dot: 'bg-rose-500',
    text: 'text-rose-600 dark:text-rose-400',
    border: 'border-rose-500/20',
  },
  idle: {
    label: 'Idle',
    dot: 'bg-muted-foreground/40',
    text: 'text-muted-foreground',
    border: 'border-border',
  },
}

function formatPercent(value: number | null | undefined): string {
  return value == null || !Number.isFinite(value) ? '—' : `${value.toFixed(2)}%`
}

function formatLatency(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value) || value <= 0) return '—'
  if (value >= 1000) return `${(value / 1000).toFixed(2)}s`
  return `${Math.round(value)}ms`
}

function formatTps(value: number | null | undefined): string {
  return value == null || !Number.isFinite(value) || value <= 0
    ? '—'
    : `${value.toFixed(1)} tok/s`
}

function formatCount(value: number, locale?: string): string {
  return new Intl.NumberFormat(locale).format(value)
}

export function ModelHealth() {
  const { t } = useTranslation()
  const [window, setWindow] = useState<ModelHealthWindow>('12h')
  const [groups, setGroups] = useState<string[]>([])
  const [models, setModels] = useState<string[]>([])
  const [pools, setPools] = useState<string[]>([])
  const [statuses, setStatuses] = useState<ModelHealthStatus[]>([])
  const filters = useMemo<ModelHealthFilters>(
    () => ({ window, groups, models, pools }),
    [groups, models, pools, window]
  )
  const query = useModelHealth(filters)
  const chartTheme = useChartTheme()

  const catalog = query.data?.catalog
  const overview = query.data?.overview
  const seriesMap = useMemo(() => {
    const map = new Map<string, ModelHealthSeriesRow>()
    for (const row of query.data?.series.rows ?? []) {
      map.set(publicHealthRowKey(row.group, row.model), row)
    }
    return map
  }, [query.data?.series.rows])
  const poolSeriesMap = useMemo(() => {
    const map = new Map<string, ModelHealthPoolSeriesRow>()
    for (const row of query.data?.series.pool_rows ?? []) {
      map.set(publicHealthPoolKey(row.group, row.model, row.pool_key), row)
    }
    return map
  }, [query.data?.series.pool_rows])

  const cards = useMemo(
    () =>
      buildModelHealthCards(
        overview?.rows ?? [],
        overview?.pool_rows ?? [],
        statuses,
        pools
      ),
    [overview?.pool_rows, overview?.rows, pools, statuses]
  )

  const rowsByStatus = useMemo(
    () =>
      STATUSES.map((status) => ({
        status,
        cards: cards.filter((card) => card.row.status === status),
      })).filter((group) => group.cards.length > 0),
    [cards]
  )

  return (
    <PublicLayout showMainContainer={false}>
      <PageTransition className='relative mx-auto w-full max-w-[1440px] space-y-5 px-3 pt-16 pb-12 sm:px-6 sm:pt-20 xl:px-8'>
        <header className='bg-card/70 overflow-hidden rounded-2xl border p-5 shadow-xs backdrop-blur sm:p-7'>
          <div className='flex flex-col justify-between gap-5 lg:flex-row lg:items-end'>
            <div>
              <div className='text-primary mb-2 flex items-center gap-2 text-sm font-medium'>
                <HeartPulse className='size-4' />
                {t('Service observability')}
              </div>
              <h1 className='text-2xl font-semibold tracking-tight sm:text-3xl'>
                {t('Model health status')}
              </h1>
              <p className='text-muted-foreground mt-2 max-w-3xl text-sm leading-6'>
                {t(
                  'Active probes show current availability while observed traffic provides real-world evidence.'
                )}
              </p>
            </div>
            <div className='flex flex-wrap items-center gap-2'>
              {WINDOWS.map((item) => (
                <Button
                  key={item}
                  size='sm'
                  variant={item === window ? 'default' : 'outline'}
                  onClick={() => setWindow(item)}
                >
                  {item}
                </Button>
              ))}
              <Button
                size='icon-sm'
                variant='ghost'
                aria-label={t('Refresh')}
                disabled={query.isFetching}
                onClick={() => void query.refetch()}
              >
                <RefreshCw
                  className={cn('size-4', query.isFetching && 'animate-spin')}
                />
              </Button>
            </div>
          </div>
        </header>

        {query.isLoading && <HealthLoading />}
        {!query.isLoading && (query.isError || !overview) && (
          <div className='bg-card rounded-2xl border border-dashed p-12 text-center'>
            <AlertTriangle className='text-muted-foreground mx-auto size-8' />
            <h2 className='mt-3 font-semibold'>
              {t('Health data unavailable')}
            </h2>
            <p className='text-muted-foreground mt-1 text-sm'>
              {t('Try refreshing this page in a moment.')}
            </p>
          </div>
        )}
        {!query.isLoading && !query.isError && overview && (
          <>
            <SummaryGrid summary={overview.summary} />

            <section className='bg-card rounded-2xl border p-4 shadow-xs'>
              <div className='grid gap-3 md:grid-cols-2 xl:grid-cols-4'>
                <MultiSelect
                  options={(catalog?.groups ?? []).map((item) => ({
                    label: item.alias,
                    value: item.alias,
                  }))}
                  selected={groups}
                  onChange={setGroups}
                  placeholder={t('All groups')}
                  maxVisibleChips={2}
                />
                <MultiSelect
                  options={(catalog?.models ?? []).map((item) => ({
                    label: item.alias,
                    value: item.alias,
                  }))}
                  selected={models}
                  onChange={setModels}
                  placeholder={t('All models')}
                  maxVisibleChips={2}
                />
                <MultiSelect
                  options={(catalog?.pools ?? []).map((item) => ({
                    label: `${item.alias} · ${item.group}`,
                    value: item.key,
                  }))}
                  selected={pools}
                  onChange={setPools}
                  placeholder={t('All pools')}
                  maxVisibleChips={2}
                />
                <MultiSelect
                  options={STATUSES.map((status) => ({
                    label: t(STATUS_META[status].label),
                    value: status,
                  }))}
                  selected={statuses}
                  onChange={(values) =>
                    setStatuses(values as ModelHealthStatus[])
                  }
                  placeholder={t('All Status')}
                  maxVisibleChips={2}
                />
              </div>
            </section>

            {rowsByStatus.length === 0 ? (
              <div className='text-muted-foreground bg-card rounded-2xl border border-dashed p-12 text-center text-sm'>
                {t('No models match the selected filters')}
              </div>
            ) : (
              <div className='space-y-5'>
                {rowsByStatus.map((group) => (
                  <StatusGroup
                    key={group.status}
                    status={group.status}
                    cards={group.cards}
                    seriesMap={seriesMap}
                    poolSeriesMap={poolSeriesMap}
                    bucketSeconds={query.data?.series.bucket_seconds ?? 0}
                    chartTheme={chartTheme}
                  />
                ))}
              </div>
            )}
          </>
        )}
      </PageTransition>
    </PublicLayout>
  )
}

function SummaryGrid(props: { summary: ModelHealthSummary }) {
  const { t, i18n } = useTranslation()
  const summary = props.summary
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const statusItems = [
    {
      icon: HeartPulse,
      label: 'Healthy',
      value: summary.healthy,
      tone: 'text-emerald-500',
    },
    {
      icon: Waves,
      label: 'Fluctuating',
      value: summary.fluctuating,
      tone: 'text-amber-500',
    },
    {
      icon: AlertTriangle,
      label: 'Unstable',
      value: summary.unstable,
      tone: 'text-rose-500',
    },
    {
      icon: CircleDashed,
      label: 'Idle',
      value: summary.idle,
      tone: 'text-muted-foreground',
    },
  ]

  return (
    <section className='grid gap-3 sm:grid-cols-2 xl:grid-cols-6'>
      <div className='bg-card rounded-2xl border p-4 shadow-xs sm:col-span-1 xl:col-span-1'>
        <div className='text-muted-foreground flex items-center gap-2 text-xs'>
          <RadioTower className='size-3.5 text-emerald-500' />
          {t('Active availability')}
        </div>
        <div className='mt-2 font-mono text-2xl font-semibold tabular-nums'>
          {formatPercent(summary.probe_availability)}
        </div>
      </div>
      <div className='bg-card rounded-2xl border p-4 shadow-xs sm:col-span-1 xl:col-span-1'>
        <div className='text-muted-foreground flex items-center gap-2 text-xs'>
          <Activity className='size-3.5 text-blue-500' />
          {t('Observed success rate')}
        </div>
        <div className='mt-2 font-mono text-2xl font-semibold tabular-nums'>
          {formatPercent(summary.observed_success_rate)}
        </div>
        <div className='text-muted-foreground mt-1 text-xs'>
          {formatCount(summary.observed_request_count, locale)} {t('Requests')}
        </div>
      </div>
      {statusItems.map((item) => (
        <div
          key={item.label}
          className='bg-card rounded-2xl border p-4 shadow-xs'
        >
          <div className='text-muted-foreground flex items-center gap-2 text-xs'>
            <item.icon className={cn('size-3.5', item.tone)} />
            {t(item.label)}
          </div>
          <div className='mt-2 font-mono text-2xl font-semibold tabular-nums'>
            {item.value}
          </div>
        </div>
      ))}
    </section>
  )
}

function StatusGroup(props: {
  status: ModelHealthStatus
  cards: ModelHealthCard[]
  seriesMap: Map<string, ModelHealthSeriesRow>
  poolSeriesMap: Map<string, ModelHealthPoolSeriesRow>
  bucketSeconds: number
  chartTheme: { resolvedTheme: string; themeReady: boolean }
}) {
  const { t } = useTranslation()
  const meta = STATUS_META[props.status]
  return (
    <section
      className={cn(
        'bg-card overflow-hidden rounded-2xl border shadow-xs',
        meta.border
      )}
    >
      <header className='flex items-center justify-between border-b px-4 py-3 sm:px-5'>
        <div className='flex items-center gap-2'>
          <span className={cn('size-2.5 rounded-full', meta.dot)} />
          <h2 className={cn('font-semibold', meta.text)}>{t(meta.label)}</h2>
        </div>
        <span className='text-muted-foreground font-mono text-xs tabular-nums'>
          {props.cards.length}
        </span>
      </header>
      <div className='divide-y'>
        {props.cards.map((card) => (
          <HealthCard
            key={publicHealthRowKey(card.row.group, card.row.model)}
            card={card}
            series={props.seriesMap.get(
              publicHealthRowKey(card.row.group, card.row.model)
            )}
            poolSeriesMap={props.poolSeriesMap}
            bucketSeconds={props.bucketSeconds}
            chartTheme={props.chartTheme}
          />
        ))}
      </div>
    </section>
  )
}

function HealthCard(props: {
  card: ModelHealthCard
  series?: ModelHealthSeriesRow
  poolSeriesMap: Map<string, ModelHealthPoolSeriesRow>
  bucketSeconds: number
  chartTheme: { resolvedTheme: string; themeReady: boolean }
}) {
  const { t } = useTranslation()
  const row = props.card.row

  return (
    <article>
      <HealthRow
        row={row}
        series={props.series}
        bucketSeconds={props.bucketSeconds}
        chartTheme={props.chartTheme}
      />
      {props.card.pools.length > 0 && (
        <Collapsible defaultOpen className='bg-muted/20 border-t'>
          <CollapsibleTrigger
            render={
              <Button
                type='button'
                variant='ghost'
                className='group h-auto w-full justify-between rounded-none px-4 py-2.5 sm:px-5'
              />
            }
          >
            <span className='text-muted-foreground text-xs font-medium'>
              {t('Pool details')} ({props.card.pools.length})
            </span>
            <ChevronDown className='text-muted-foreground size-4 transition-transform group-aria-expanded:rotate-180' />
          </CollapsibleTrigger>
          <CollapsibleContent className='divide-y border-t'>
            {props.card.pools.map((pool) => (
              <HealthRow
                key={publicHealthPoolKey(pool.group, pool.model, pool.pool_key)}
                row={pool}
                pool={pool}
                series={props.poolSeriesMap.get(
                  publicHealthPoolKey(pool.group, pool.model, pool.pool_key)
                )}
                bucketSeconds={props.bucketSeconds}
                chartTheme={props.chartTheme}
              />
            ))}
          </CollapsibleContent>
        </Collapsible>
      )}
    </article>
  )
}

function HealthRow(props: {
  row: ModelHealthRow
  series?: ModelHealthSeriesRow
  pool?: ModelHealthPoolRow
  bucketSeconds: number
  chartTheme: { resolvedTheme: string; themeReady: boolean }
}) {
  const { t, i18n } = useTranslation()
  const row = props.row
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const title = props.pool?.pool ?? row.group
  const subtitle = props.pool ? `${row.group} · ${row.model}` : row.model
  return (
    <div
      className={cn(
        'grid gap-4 px-4 py-4 sm:px-5',
        props.pool && 'bg-background/60 pl-6 sm:pl-8'
      )}
    >
      <div className='grid gap-4 lg:grid-cols-[minmax(13rem,0.7fr)_minmax(18rem,2fr)] lg:items-center'>
        <div className='min-w-0'>
          <div className='flex min-w-0 flex-wrap items-center gap-2'>
            <span
              className='truncate font-mono text-sm font-semibold'
              title={title}
            >
              {title}
            </span>
            {props.pool && (
              <Badge
                variant='outline'
                className={STATUS_META[row.status].border}
              >
                <span
                  className={cn(
                    'size-1.5 rounded-full',
                    STATUS_META[row.status].dot
                  )}
                />
                <span className={STATUS_META[row.status].text}>
                  {t(STATUS_META[row.status].label)}
                </span>
              </Badge>
            )}
            {row.signal_conflict && (
              <Badge
                variant='outline'
                className='border-amber-500/30 text-amber-600 dark:text-amber-400'
              >
                <AlertTriangle className='size-3' />
                {t('Signal mismatch')}
              </Badge>
            )}
          </div>
          <div
            className='text-muted-foreground mt-1 truncate text-xs'
            title={subtitle}
          >
            {subtitle}
          </div>
        </div>

        <HealthTrend
          points={props.series?.points ?? []}
          bucketSeconds={props.bucketSeconds}
          resolvedTheme={props.chartTheme.resolvedTheme}
          themeReady={props.chartTheme.themeReady}
          observedLabel={
            props.pool ? t('Channel attempt success rate') : undefined
          }
        />
      </div>

      <div className='grid grid-cols-[repeat(auto-fit,minmax(8rem,1fr))] gap-x-5 gap-y-3 text-xs'>
        <Metric
          label={t('Active availability')}
          value={formatPercent(row.probe.availability)}
          icon={RadioTower}
        />
        <Metric
          label={t('Latest latency')}
          value={formatLatency(row.probe.latest_latency_ms)}
          icon={Timer}
        />
        <Metric
          label={
            props.pool
              ? t('Channel attempt success rate')
              : t('Observed success rate')
          }
          value={formatPercent(row.observed.success_rate)}
          icon={Activity}
        />
        <Metric
          label={props.pool ? t('Channel attempts') : t('Requests')}
          value={formatCount(row.observed.request_count, locale)}
          icon={Waves}
        />
        <Metric
          label={t('Observed latency')}
          value={formatLatency(row.observed.avg_latency_ms)}
          icon={Timer}
        />
        <Metric
          label='TTFT'
          value={formatLatency(row.observed.avg_ttft_ms)}
          icon={Clock3}
        />
        <Metric
          label='TPS'
          value={formatTps(row.observed.avg_tps)}
          icon={Activity}
        />
      </div>
    </div>
  )
}

function Metric(props: {
  label: string
  value: string
  icon: React.ComponentType<{ className?: string }>
}) {
  const Icon = props.icon
  return (
    <div className='min-w-0'>
      <div className='text-muted-foreground flex items-center gap-1.5 leading-tight'>
        <Icon className='size-3 shrink-0' />
        {props.label}
      </div>
      <div className='mt-1 font-mono font-semibold whitespace-nowrap tabular-nums'>
        {props.value}
      </div>
    </div>
  )
}

function HealthLoading() {
  return (
    <div className='space-y-5'>
      <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-6'>
        {Array.from({ length: 6 }, (_, index) => (
          <Skeleton key={index} className='h-24 rounded-2xl' />
        ))}
      </div>
      <Skeleton className='h-16 rounded-2xl' />
      <Skeleton className='h-80 rounded-2xl' />
    </div>
  )
}
