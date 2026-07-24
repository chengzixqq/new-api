import {
  CONTENT_DEFAULT_SECTION,
  type CONTENT_SECTION_IDS,
} from '../section-route-config'
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
import type { ContentSettings } from '../types'
import { createSectionRegistry } from '../utils/section-registry'
import { AnnouncementsSection } from './announcements-section'
import { ApiInfoSection } from './api-info-section'
import { ChatSettingsSection } from './chat-settings-section'
import { DashboardSection } from './dashboard-section'
import { DrawingSettingsSection } from './drawing-settings-section'
import { FAQSection } from './faq-section'
import { ModelHealthSection } from './model-health-section'

/**
 * Validate and coerce DataExportDefaultTime to a safe value
 */
function validateDataExportDefaultTime(value: string): 'week' | 'hour' | 'day' {
  if (value === 'week' || value === 'hour' || value === 'day') {
    return value
  }
  // Default to 'hour' if value is unexpected
  return 'hour'
}

const CONTENT_SECTIONS = [
  {
    id: 'dashboard',
    titleKey: 'Data Dashboard',
    build: (settings: ContentSettings) => (
      <DashboardSection
        defaultValues={{
          DataExportEnabled: settings.DataExportEnabled,
          DataExportInterval: settings.DataExportInterval,
          DataExportDefaultTime: validateDataExportDefaultTime(
            settings.DataExportDefaultTime
          ),
        }}
      />
    ),
  },
  {
    id: 'announcements',
    titleKey: 'Announcements',
    build: (settings: ContentSettings) => (
      <AnnouncementsSection
        enabled={settings['console_setting.announcements_enabled']}
        data={settings['console_setting.announcements']}
      />
    ),
  },
  {
    id: 'api-info',
    titleKey: 'API Addresses',
    build: (settings: ContentSettings) => (
      <ApiInfoSection
        enabled={settings['console_setting.api_info_enabled']}
        data={settings['console_setting.api_info']}
      />
    ),
  },
  {
    id: 'faq',
    titleKey: 'FAQ',
    build: (settings: ContentSettings) => (
      <FAQSection
        enabled={settings['console_setting.faq_enabled']}
        data={settings['console_setting.faq']}
      />
    ),
  },
  {
    id: 'model-health',
    titleKey: 'Model health',
    build: (settings: ContentSettings) => (
      <ModelHealthSection
        defaultValues={{
          'model_health_setting.enabled':
            settings['model_health_setting.enabled'],
          'model_health_setting.pool_details_enabled':
            settings['model_health_setting.pool_details_enabled'],
          'model_health_setting.multi_sample_enabled':
            settings['model_health_setting.multi_sample_enabled'],
          'model_health_setting.default_interval_seconds':
            settings['model_health_setting.default_interval_seconds'],
          'model_health_setting.default_timeout_seconds':
            settings['model_health_setting.default_timeout_seconds'],
          'model_health_setting.default_sampling_mode':
            settings['model_health_setting.default_sampling_mode'],
          'model_health_setting.default_samples_per_run':
            settings['model_health_setting.default_samples_per_run'],
          'model_health_setting.default_minimum_successes':
            settings['model_health_setting.default_minimum_successes'],
          'model_health_setting.default_sample_spacing_seconds':
            settings['model_health_setting.default_sample_spacing_seconds'],
          'model_health_setting.concurrency':
            settings['model_health_setting.concurrency'],
          'model_health_setting.retention_days':
            settings['model_health_setting.retention_days'],
          'model_health_setting.healthy_threshold':
            settings['model_health_setting.healthy_threshold'],
          'model_health_setting.fluctuating_threshold':
            settings['model_health_setting.fluctuating_threshold'],
          'model_health_setting.passive_min_samples':
            settings['model_health_setting.passive_min_samples'],
          'model_health_setting.active_min_samples':
            settings['model_health_setting.active_min_samples'],
          'model_health_setting.public_groups':
            settings['model_health_setting.public_groups'],
          'model_health_setting.public_models':
            settings['model_health_setting.public_models'],
          'perf_metrics_setting.enabled':
            settings['perf_metrics_setting.enabled'],
          'perf_metrics_setting.flush_interval':
            settings['perf_metrics_setting.flush_interval'],
          'perf_metrics_setting.bucket_time':
            settings['perf_metrics_setting.bucket_time'],
          'perf_metrics_setting.retention_days':
            settings['perf_metrics_setting.retention_days'],
        }}
      />
    ),
  },
  {
    id: 'chat',
    titleKey: 'Chat Presets',
    build: (settings: ContentSettings) => (
      <ChatSettingsSection defaultValue={settings.Chats} />
    ),
  },
  {
    id: 'drawing',
    titleKey: 'Drawing',
    build: (settings: ContentSettings) => (
      <DrawingSettingsSection
        defaultValues={{
          DrawingEnabled: settings.DrawingEnabled,
          MjNotifyEnabled: settings.MjNotifyEnabled,
          MjAccountFilterEnabled: settings.MjAccountFilterEnabled,
          MjForwardUrlEnabled: settings.MjForwardUrlEnabled,
          MjModeClearEnabled: settings.MjModeClearEnabled,
          MjActionCheckSuccessEnabled: settings.MjActionCheckSuccessEnabled,
        }}
      />
    ),
  },
] as const

export type ContentSectionId = (typeof CONTENT_SECTION_IDS)[number]

const contentRegistry = createSectionRegistry<
  ContentSectionId,
  ContentSettings
>({
  sections: CONTENT_SECTIONS,
  defaultSection: CONTENT_DEFAULT_SECTION,
  basePath: '/system-settings/content',
  urlStyle: 'path',
})

export const getContentSectionNavItems = contentRegistry.getSectionNavItems
export const getContentSectionContent = contentRegistry.getSectionContent
export const getContentSectionMeta = contentRegistry.getSectionMeta
export {
  CONTENT_DEFAULT_SECTION,
  CONTENT_SECTION_IDS,
} from '../section-route-config'
