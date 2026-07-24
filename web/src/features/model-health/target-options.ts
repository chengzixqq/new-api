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
import type {
  ModelHealthAdminChannelOption,
  ModelHealthAdminGroupOption,
  ModelHealthAdminTokenOption,
  ModelHealthTarget,
  ModelHealthTargetModel,
} from './types'

export function hasPublicPoolAliasConflict(
  targets: Array<
    Pick<
      ModelHealthTarget,
      'id' | 'publish_pool_detail' | 'public_group_alias' | 'public_pool_alias'
    >
  >,
  currentTargetID: number | undefined,
  groupAlias: string,
  poolAlias: string
): boolean {
  const group = groupAlias.trim()
  const pool = poolAlias.trim()
  if (!group || !pool) return false
  return targets.some(
    (target) =>
      target.id !== currentTargetID &&
      target.publish_pool_detail &&
      target.public_group_alias.trim() === group &&
      target.public_pool_alias.trim() === pool
  )
}

export function modelHealthChannelLabel(
  channel: ModelHealthAdminChannelOption,
  unavailableLabel: string
): string {
  if (channel.available === false || channel.status !== 1) {
    return `${channel.name} (#${channel.id}, ${unavailableLabel})`
  }
  return `${channel.name} (#${channel.id})`
}

export function selectableChannelsForScope(
  channels: ModelHealthAdminChannelOption[],
  selectedGroups: string[],
  selectedModels: string[]
): ModelHealthAdminChannelOption[] {
  const groupSet = new Set(selectedGroups)
  const modelSet = new Set(selectedModels)
  return channels.filter((channel) => {
    if (channel.available === false || channel.status !== 1) return false
    const groupMatches =
      groupSet.size === 0 || channel.groups.some((group) => groupSet.has(group))
    const modelMatches =
      modelSet.size === 0 || channel.models.some((model) => modelSet.has(model))
    return groupMatches && modelMatches
  })
}

export function modelHealthGroupLabel(
  group: ModelHealthAdminGroupOption
): string {
  return group.name
}

export function selectableModelsForGroups(
  groups: ModelHealthAdminGroupOption[],
  selectedGroups: string[]
): string[] {
  const selected = groups.filter((group) => selectedGroups.includes(group.name))
  if (selected.length === 0) {
    return [...new Set(groups.flatMap((group) => group.models))].sort()
  }

  let common = new Set(selected[0]?.models ?? [])
  for (const group of selected.slice(1)) {
    const available = new Set(group.models)
    common = new Set([...common].filter((model) => available.has(model)))
  }
  return [...common].sort()
}

export function syncTargetModels(
  current: ModelHealthTargetModel[],
  selectedNames: string[]
): ModelHealthTargetModel[] {
  const currentByName = new Map(current.map((model) => [model.name, model]))
  return [...new Set(selectedNames)].map((name) => {
    const saved = currentByName.get(name)
    if (!saved) {
      return { name, public_alias: name, required: true }
    }
    return {
      ...saved,
      public_alias: saved.public_alias || name,
    }
  })
}

export function modelsAllowedByProbeToken(
  models: string[],
  token?: ModelHealthAdminTokenOption
): string[] {
  if (!token?.model_limits_enabled) return models
  const allowed = new Set(token.model_limits)
  return models.filter((model) => allowed.has(model))
}

export function automaticGroupAlias(
  _groups: ModelHealthAdminGroupOption[],
  selectedGroups: string[]
): string {
  return selectedGroups.join(' / ')
}

export function automaticTargetName(
  mode: 'local' | 'upstream',
  groupAlias: string,
  modelNames: string[]
): string {
  const scope = groupAlias || modelNames[0] || 'model-health'
  return `${mode}-${scope}`.slice(0, 128)
}
