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
import i18next from 'i18next'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'

import { useStatus } from '@/hooks/use-status'
import { isHttpUrl } from '@/lib/content-format'

import { getHomePageContent } from '../api'
import type { HomePageContentResult } from '../types'

const STORAGE_KEY = 'home_page_content:v2'
const LEGACY_STORAGE_KEY = 'home_page_content'

/**
 * Hook to load and manage custom home page content
 * Supports both Markdown/HTML content and iframe URLs
 */
export function useHomePageContent(): HomePageContentResult {
  const { status } = useStatus()
  const [state, setState] = useState(() => {
    try {
      const cached = localStorage.getItem(STORAGE_KEY)
      if (cached !== null) {
        return { content: cached, isLoaded: true }
      }

      const legacy = localStorage.getItem(LEGACY_STORAGE_KEY)
      if (legacy !== null) {
        localStorage.setItem(STORAGE_KEY, legacy)
        localStorage.removeItem(LEGACY_STORAGE_KEY)
        return { content: legacy, isLoaded: true }
      }
    } catch {
      /* empty */
    }
    return { content: '', isLoaded: false }
  })
  const useDefaultHome = status?.home_page_content_enabled === false

  useEffect(() => {
    if (useDefaultHome) {
      try {
        localStorage.setItem(STORAGE_KEY, '')
        localStorage.removeItem(LEGACY_STORAGE_KEY)
      } catch {
        /* empty */
      }
      setState({ content: '', isLoaded: true })
      return
    }

    let mounted = true

    const loadContent = async () => {
      try {
        const response = await getHomePageContent()
        const { success, data } = response

        if (!mounted) return

        const content = success && typeof data === 'string' ? data : ''
        setState({ content, isLoaded: true })
        try {
          localStorage.setItem(STORAGE_KEY, content)
          localStorage.removeItem(LEGACY_STORAGE_KEY)
        } catch {
          /* empty */
        }
      } catch (error) {
        if (!mounted) return
        // eslint-disable-next-line no-console
        console.error('Failed to load home page content:', error)
        toast.error(i18next.t('Failed to load home page content'))
      } finally {
        if (mounted) {
          setState((current) => ({ ...current, isLoaded: true }))
        }
      }
    }

    loadContent()

    return () => {
      mounted = false
    }
  }, [useDefaultHome])

  const content = useDefaultHome ? '' : state.content
  const isUrl = isHttpUrl(content)

  return {
    content,
    isLoaded: useDefaultHome || state.isLoaded,
    isUrl,
  }
}
