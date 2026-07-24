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
/**
 * Utility functions for usage logs feature
 */
import {
  getAllLogs,
  getUserLogs,
  getAllMidjourneyLogs,
  getUserMidjourneyLogs,
  getAllTaskLogs,
  getUserTaskLogs,
} from '../api'
import {
  LOG_TYPES,
  DISPLAYABLE_LOG_TYPES,
  TIMING_LOG_TYPES,
} from '../constants'
import type {
  GetLogsParams,
  GetLogsResponse,
  FetchLogsConfig,
  GetLogStatsParams,
  GetMidjourneyLogsParams,
  GetTaskLogsParams,
} from '../types'
import { buildTimeRangeParams } from './time-range'

export { getDefaultTimeRange } from './time-range'

// ============================================================================
// Type Checkers & Utilities
// ============================================================================

/**
 * Check if log type is displayable (has detailed info)
 */
export function isDisplayableLogType(type: number): boolean {
  return (DISPLAYABLE_LOG_TYPES as readonly number[]).includes(type)
}

/**
 * Check if log type shows timing info
 */
export function isTimingLogType(type: number): boolean {
  return (TIMING_LOG_TYPES as readonly number[]).includes(type)
}

/**
 * Get log type configuration by type number
 */
export function getLogTypeConfig(type: number) {
  return LOG_TYPES.find((t) => t.value === type) || LOG_TYPES[0]
}

/**
 * Check if log uses per-call billing
 */
export function isPerCallBilling(modelPrice?: number): boolean {
  return (modelPrice ?? 0) > 0
}

/**
 * Build base parameters with time range (for drawing and task logs)
 * @param useMilliseconds - Whether to use millisecond timestamps (true for drawing logs, false for task logs)
 */
export function buildBaseParams(config: {
  page: number
  pageSize: number
  searchParams: Record<string, unknown>
  useMilliseconds?: boolean
}): {
  p: number
  page_size: number
  channel_id?: string
  start_timestamp?: number
  end_timestamp?: number
} {
  const { page, pageSize, searchParams, useMilliseconds = false } = config

  return {
    p: page,
    page_size: pageSize,
    ...(searchParams.channel
      ? {
          channel_id: String(searchParams.channel),
        }
      : {}),
    ...buildTimeRangeParams(searchParams, useMilliseconds),
  }
}

/**
 * Build API params from search params and column filters (for common logs)
 */
export function buildApiParams(config: {
  page: number
  pageSize: number
  searchParams: Record<string, unknown>
  isAdmin: boolean
}): GetLogsParams {
  const { page, pageSize, searchParams, isAdmin } = config

  // Helper to process type parameter (single value from array)
  const processType = (value: unknown): number | undefined => {
    const parseType = (raw: unknown): number | undefined => {
      const type = Number(raw)
      return Number.isFinite(type) ? type : undefined
    }

    if (Array.isArray(value) && value.length === 1) {
      return parseType(value[0])
    }
    if (typeof value === 'string' && value !== '') {
      return parseType(value)
    }
    return undefined
  }

  // Build base params from search params
  const params: GetLogsParams = {
    p: page,
    page_size: pageSize,
    ...(searchParams.type ? { type: processType(searchParams.type) } : {}),
    ...(searchParams.model ? { model_name: String(searchParams.model) } : {}),
    ...(searchParams.token ? { token_name: String(searchParams.token) } : {}),
    ...(searchParams.group ? { group: String(searchParams.group) } : {}),
    ...(isAdmin && searchParams.channel
      ? { channel: Number(searchParams.channel) || 0 }
      : {}),
    ...(isAdmin && searchParams.username
      ? { username: String(searchParams.username) }
      : {}),
    ...(searchParams.requestId
      ? { request_id: String(searchParams.requestId) }
      : {}),
    ...(searchParams.upstreamRequestId
      ? { upstream_request_id: String(searchParams.upstreamRequestId) }
      : {}),
    ...buildTimeRangeParams(searchParams, false),
  }

  return params
}

export function buildLogStatsParams(
  searchParams: Record<string, unknown>,
  isAdmin: boolean
): GetLogStatsParams {
  const params = buildApiParams({
    page: 1,
    pageSize: 1,
    searchParams,
    isAdmin,
  })
  delete params.p
  delete params.page_size
  return params
}

export type LogsRequestParams =
  | GetLogsParams
  | GetMidjourneyLogsParams
  | GetTaskLogsParams

export function buildLogsRequestParams(
  config: FetchLogsConfig
): LogsRequestParams {
  if (config.logCategory === 'common') {
    return buildApiParams({
      page: config.page,
      pageSize: config.pageSize,
      searchParams: config.searchParams,
      isAdmin: config.isAdmin,
    })
  }

  const baseParams = buildBaseParams({
    page: config.page,
    pageSize: config.pageSize,
    searchParams: config.searchParams,
    useMilliseconds: config.logCategory === 'drawing',
  })

  if (config.logCategory === 'drawing') {
    return {
      ...baseParams,
      mj_id: config.searchParams.filter as string | undefined,
    }
  }

  return {
    ...baseParams,
    task_id: config.searchParams.filter as string | undefined,
  }
}

// ============================================================================
// Data Fetching
// ============================================================================

/**
 * Fetch logs based on category type
 */
export async function fetchLogsByCategory(
  config: FetchLogsConfig
): Promise<GetLogsResponse> {
  const params = buildLogsRequestParams(config)

  if (config.logCategory === 'common') {
    const commonParams = params as GetLogsParams
    return config.isAdmin
      ? await getAllLogs(commonParams)
      : await getUserLogs(commonParams)
  }

  if (config.logCategory === 'drawing') {
    const drawingParams = params as GetMidjourneyLogsParams
    return config.isAdmin
      ? await getAllMidjourneyLogs(drawingParams)
      : await getUserMidjourneyLogs(drawingParams)
  }

  const taskParams = params as GetTaskLogsParams
  return config.isAdmin
    ? await getAllTaskLogs(taskParams)
    : await getUserTaskLogs(taskParams)
}
