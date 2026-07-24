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
import type { LogCategory } from '../types'

const FILTER_SEARCH_KEYS = [
  'type',
  'filter',
  'model',
  'token',
  'channel',
  'group',
  'username',
  'requestId',
  'upstreamRequestId',
  'startTime',
  'endTime',
] as const

type NormalizedParam = readonly [key: string, value: string]

function normalizePositiveInteger(
  value: unknown,
  fallback: number | undefined,
  maximum = Number.MAX_SAFE_INTEGER
): number | undefined {
  if (value === undefined || value === null || value === '') return fallback
  const number = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(number)) return fallback
  return Math.min(maximum, Math.max(1, Math.trunc(number)))
}

export function normalizeUsageLogPage(value: unknown): number {
  return normalizePositiveInteger(value, 1) ?? 1
}

export function normalizeUsageLogPageSize(value: unknown): number | undefined {
  return normalizePositiveInteger(value, undefined, 100)
}

function normalizeValue(value: unknown): string {
  return Array.isArray(value)
    ? value.map((item) => String(item)).join('\u001e')
    : String(value)
}

export function normalizeUsageLogParams(params: object): NormalizedParam[] {
  return Object.entries(params)
    .filter(
      ([, value]) => value !== undefined && value !== null && value !== ''
    )
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => [key, normalizeValue(value)] as const)
}

export function buildUsageLogQueryParams(
  params: Record<string, unknown>
): URLSearchParams {
  const queryParams = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== '') {
      queryParams.append(key, String(value))
    }
  }
  return queryParams
}

export function createUsageLogsQueryKey(
  logCategory: LogCategory,
  isAdmin: boolean,
  params: object
) {
  return [
    'logs',
    logCategory,
    isAdmin,
    normalizeUsageLogParams(params),
  ] as const
}

export function createUsageLogStatsQueryKey(isAdmin: boolean, params: object) {
  const entries = Object.entries(params).filter(
    ([key]) => key !== 'p' && key !== 'page_size'
  )
  return [
    'usage-logs-stats',
    isAdmin,
    normalizeUsageLogParams(Object.fromEntries(entries)),
  ] as const
}

export function createUsageLogsFilterSourceKey(
  search: Record<string, unknown>
): string {
  return FILTER_SEARCH_KEYS.map((key) =>
    normalizeValue(search[key] ?? '')
  ).join('\u001f')
}

export function buildUsageLogApiPath(
  endpoint: string,
  isAdmin: boolean,
  resource: 'list' | 'stat'
): string {
  const base = endpoint.replace(/\/+$/, '')
  if (resource === 'stat') {
    return isAdmin ? `${base}/stat` : `${base}/self/stat`
  }
  return isAdmin ? `${base}/` : `${base}/self`
}
