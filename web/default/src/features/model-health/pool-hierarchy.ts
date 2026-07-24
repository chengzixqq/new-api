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
  ModelHealthPoolRow,
  ModelHealthRow,
  ModelHealthStatus,
} from './types'

export type ModelHealthCard = {
  row: ModelHealthRow
  pools: ModelHealthPoolRow[]
}

export function publicHealthRowKey(group: string, model: string): string {
  return `${group}\u0000${model}`
}

export function publicHealthPoolKey(
  group: string,
  model: string,
  poolKey: string
): string {
  return `${group}\u0000${model}\u0000${poolKey}`
}

export function buildModelHealthCards(
  rows: ModelHealthRow[],
  poolRows: ModelHealthPoolRow[],
  selectedStatuses: ModelHealthStatus[],
  selectedPools: string[]
): ModelHealthCard[] {
  const poolsByRow = new Map<string, ModelHealthPoolRow[]>()
  const selectedPoolSet = new Set(selectedPools)
  for (const pool of poolRows) {
    if (selectedPoolSet.size > 0 && !selectedPoolSet.has(pool.pool_key)) {
      continue
    }
    const key = publicHealthRowKey(pool.group, pool.model)
    const current = poolsByRow.get(key) ?? []
    current.push(pool)
    poolsByRow.set(key, current)
  }

  const statusSet = new Set(selectedStatuses)
  const cards: ModelHealthCard[] = []
  for (const row of rows) {
    const rowPools =
      poolsByRow.get(publicHealthRowKey(row.group, row.model)) ?? []
    if (selectedPoolSet.size > 0 && rowPools.length === 0) continue
    if (statusSet.size === 0) {
      cards.push({ row, pools: rowPools })
      continue
    }
    if (statusSet.has(row.status)) {
      cards.push({ row, pools: rowPools })
      continue
    }
    const matchingPools = rowPools.filter((pool) => statusSet.has(pool.status))
    if (matchingPools.length > 0) {
      cards.push({ row, pools: matchingPools })
    }
  }
  return cards
}
