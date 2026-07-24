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
import { api } from '@/lib/api'

import type {
  ApiResponse,
  ModelHealthAdminOptions,
  ModelHealthCatalog,
  ModelHealthFilters,
  ModelHealthOverview,
  ModelHealthSeries,
  ModelHealthProbeToken,
  ModelHealthTarget,
  ModelHealthTargetPayload,
  ModelHealthTargetValidationResult,
} from './types'

function requireSuccess<T>(response: ApiResponse<T>): ApiResponse<T> {
  if (!response.success) {
    throw new Error(response.message || 'Request failed')
  }
  return response
}

function buildPublicQuery(filters: ModelHealthFilters): string {
  const params = new URLSearchParams({ window: filters.window })
  filters.groups.forEach((group) => params.append('group', group))
  filters.models.forEach((model) => params.append('model', model))
  filters.pools.forEach((pool) => params.append('pool', pool))
  return params.toString()
}

export async function getModelHealthCatalog() {
  const response = await api.get<ApiResponse<ModelHealthCatalog>>(
    '/api/model-health/catalog'
  )
  return requireSuccess(response.data)
}

export async function getModelHealthOverview(filters: ModelHealthFilters) {
  const response = await api.get<ApiResponse<ModelHealthOverview>>(
    `/api/model-health/overview?${buildPublicQuery(filters)}`
  )
  return requireSuccess(response.data)
}

export async function getModelHealthSeries(filters: ModelHealthFilters) {
  const response = await api.get<ApiResponse<ModelHealthSeries>>(
    `/api/model-health/series?${buildPublicQuery(filters)}`
  )
  return requireSuccess(response.data)
}

export async function getModelHealthTargets() {
  const response = await api.get<ApiResponse<ModelHealthTarget[]>>(
    '/api/model-health/admin/targets'
  )
  return requireSuccess(response.data)
}

export async function getModelHealthAdminOptions() {
  const response = await api.get<ApiResponse<ModelHealthAdminOptions>>(
    '/api/model-health/admin/options'
  )
  return requireSuccess(response.data)
}

export async function getModelHealthProbeTokens() {
  const response = await api.get<ApiResponse<ModelHealthProbeToken[]>>(
    '/api/model-health/admin/probe-tokens'
  )
  return requireSuccess(response.data)
}

export async function setModelHealthProbeToken(id: number, marked: boolean) {
  const response = await api.put<ApiResponse<{ id: number; marked: boolean }>>(
    `/api/model-health/admin/probe-tokens/${id}`,
    { marked }
  )
  return requireSuccess(response.data)
}

export async function createModelHealthTarget(
  payload: ModelHealthTargetPayload
) {
  const response = await api.post<ApiResponse<ModelHealthTarget>>(
    '/api/model-health/admin/targets',
    payload
  )
  return requireSuccess(response.data)
}

export async function updateModelHealthTarget(
  id: number,
  payload: ModelHealthTargetPayload
) {
  const response = await api.put<ApiResponse<ModelHealthTarget>>(
    `/api/model-health/admin/targets/${id}`,
    payload
  )
  return requireSuccess(response.data)
}

export async function deleteModelHealthTarget(id: number) {
  const response = await api.delete<ApiResponse<null>>(
    `/api/model-health/admin/targets/${id}`
  )
  return requireSuccess(response.data)
}

export async function validateModelHealthTarget(
  payload: ModelHealthTargetPayload
) {
  const response = await api.post<
    ApiResponse<ModelHealthTargetValidationResult>
  >('/api/model-health/admin/targets/validate', payload)
  if (!response.data.data) {
    throw new Error(response.data.message || 'Request failed')
  }
  return response.data
}

export async function runModelHealthTarget(id: number) {
  const response = await api.post<ApiResponse<{ task_id?: string }>>(
    `/api/model-health/admin/targets/${id}/run`
  )
  return requireSuccess(response.data)
}

export async function runAllModelHealthTargets() {
  const response = await api.post<ApiResponse<{ task_id?: string }>>(
    '/api/model-health/admin/run'
  )
  return requireSuccess(response.data)
}
