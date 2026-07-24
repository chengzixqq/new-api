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
import { queryOptions } from '@tanstack/react-query'

import type { SystemStatus } from '@/features/auth/types'
import { getStatus } from '@/lib/api'
import { DEFAULT_LOGO, DEFAULT_SYSTEM_NAME } from '@/lib/constants'
import { applyFaviconToDom } from '@/lib/dom-utils'
import {
  DEFAULT_CURRENCY_CONFIG,
  type CurrencyConfig,
  type CurrencyDisplayType,
  type SystemConfig,
  useSystemConfigStore,
} from '@/stores/system-config-store'

export const statusQueryKey = ['status'] as const

function toNumber(value: unknown, fallback: number): number {
  if (typeof value === 'number' && !Number.isNaN(value)) return value
  if (typeof value === 'string') {
    const parsed = Number(value)
    if (!Number.isNaN(parsed)) return parsed
  }
  return fallback
}

/** Map `/api/status` data to the persisted system configuration store. */
export function mapStatusDataToConfig(
  data: SystemStatus | undefined
): Partial<SystemConfig> {
  if (!data) return {}

  const quotaDisplayType =
    (data.quota_display_type as CurrencyDisplayType | undefined) ??
    DEFAULT_CURRENCY_CONFIG.quotaDisplayType

  const currency: CurrencyConfig = {
    displayInCurrency:
      data.display_in_currency ?? DEFAULT_CURRENCY_CONFIG.displayInCurrency,
    quotaDisplayType,
    quotaPerUnit: toNumber(
      data.quota_per_unit,
      DEFAULT_CURRENCY_CONFIG.quotaPerUnit
    ),
    usdExchangeRate: toNumber(
      data.usd_exchange_rate,
      DEFAULT_CURRENCY_CONFIG.usdExchangeRate
    ),
    customCurrencySymbol:
      data.custom_currency_symbol?.trim() ||
      DEFAULT_CURRENCY_CONFIG.customCurrencySymbol,
    customCurrencyExchangeRate: toNumber(
      data.custom_currency_exchange_rate,
      DEFAULT_CURRENCY_CONFIG.customCurrencyExchangeRate
    ),
  }

  return {
    systemName: data.system_name || DEFAULT_SYSTEM_NAME,
    logo: data.logo || DEFAULT_LOGO,
    footerHtml:
      typeof data.footer_html === 'string' ? data.footer_html : undefined,
    demoSiteEnabled: data.demo_site_enabled,
    displayTokenStatEnabled: data.display_token_stat_enabled,
    currency,
  }
}

export function getCachedSystemStatus(): SystemStatus | undefined {
  try {
    if (typeof window === 'undefined') return undefined
    const saved = window.localStorage.getItem('status')
    if (!saved) return undefined

    const parsed: unknown = JSON.parse(saved)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return undefined
    }
    return parsed as SystemStatus
  } catch {
    return undefined
  }
}

export function clearCachedSystemStatus(): void {
  try {
    if (typeof window !== 'undefined') {
      window.localStorage.removeItem('status')
    }
  } catch {
    /* empty */
  }
}

export function applySystemStatus(
  status: SystemStatus | null | undefined,
  persist = true
): void {
  if (!status) return

  const systemConfig = useSystemConfigStore.getState()
  systemConfig.setConfig(mapStatusDataToConfig(status))
  systemConfig.setLoading(false)

  try {
    if (persist && typeof window !== 'undefined') {
      window.localStorage.setItem('status', JSON.stringify(status))
    }
  } catch {
    /* empty */
  }

  if (typeof document === 'undefined') return
  if (status.system_name) {
    document.title = status.system_name
    const metaTitle = document.querySelector(
      'meta[name="title"]'
    ) as HTMLMetaElement | null
    metaTitle?.setAttribute('content', status.system_name)
  }
  if (status.logo) applyFaviconToDom(status.logo)
}

async function fetchSystemStatus(
  signal?: AbortSignal
): Promise<SystemStatus | null> {
  try {
    const status = (await getStatus(signal)) as SystemStatus | null
    applySystemStatus(status)
    return status
  } finally {
    useSystemConfigStore.getState().setLoading(false)
  }
}

export const statusQueryOptions = queryOptions({
  queryKey: statusQueryKey,
  queryFn: ({ signal }) => fetchSystemStatus(signal),
  placeholderData: getCachedSystemStatus,
  staleTime: 5 * 60 * 1000,
  gcTime: 30 * 60 * 1000,
})
