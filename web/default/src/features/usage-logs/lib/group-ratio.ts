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
import type { LogOtherData } from '../types'

export type LogGroupRatioKind = 'personal' | 'membership' | 'group'

export type ResolvedLogGroupRatio = {
  ratio: number
  kind: LogGroupRatioKind
}

export function getLogGroupRatioLabelKey(kind: LogGroupRatioKind): string {
  if (kind === 'personal') return 'Personal group pricing'
  if (kind === 'membership') return 'User Exclusive Ratio'
  return 'Group Ratio'
}

function isUsableRatio(value: number | undefined): value is number {
  return value != null && value !== -1 && Number.isFinite(value)
}

export function resolveLogGroupRatio(
  other: LogOtherData | null
): ResolvedLogGroupRatio | null {
  if (!other) return null

  if (isUsableRatio(other.user_group_ratio_override)) {
    return { ratio: other.user_group_ratio_override, kind: 'personal' }
  }

  if (isUsableRatio(other.group_ratio)) {
    if (other.group_ratio_source === 'user_group_ratio_override') {
      return { ratio: other.group_ratio, kind: 'personal' }
    }
    if (other.group_ratio_source === 'group_group_ratio') {
      return { ratio: other.group_ratio, kind: 'membership' }
    }
    return { ratio: other.group_ratio, kind: 'group' }
  }

  // Compatibility with logs written before the effective ratio/source fields.
  if (isUsableRatio(other.user_group_ratio)) {
    return { ratio: other.user_group_ratio, kind: 'membership' }
  }

  return null
}
