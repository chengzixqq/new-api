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
export const AUTH_SECTION_IDS = [
  'basic-auth',
  'oauth',
  'passkey',
  'bot-protection',
  'custom-oauth',
] as const
export const AUTH_DEFAULT_SECTION = AUTH_SECTION_IDS[0]

export const BILLING_SECTION_IDS = [
  'quota',
  'currency',
  'model-pricing',
  'group-pricing',
  'payment',
  'checkin',
] as const
export const BILLING_DEFAULT_SECTION = BILLING_SECTION_IDS[0]

export const CONTENT_SECTION_IDS = [
  'dashboard',
  'announcements',
  'api-info',
  'faq',
  'model-health',
  'chat',
  'drawing',
] as const
export const CONTENT_DEFAULT_SECTION = CONTENT_SECTION_IDS[0]

export const MODELS_SECTION_IDS = [
  'global',
  'routing-reliability',
  'gemini',
  'claude',
  'grok',
  'channel-affinity',
  'model-deployment',
] as const
export const MODELS_DEFAULT_SECTION = MODELS_SECTION_IDS[0]

export const OPERATIONS_SECTION_IDS = [
  'behavior',
  'alerts',
  'email',
  'worker',
  'logs',
  'performance',
  'update-checker',
] as const
export const OPERATIONS_DEFAULT_SECTION = OPERATIONS_SECTION_IDS[0]

export const SECURITY_SECTION_IDS = [
  'rate-limit',
  'sensitive-words',
  'ssrf',
  'token-limits',
] as const
export const SECURITY_DEFAULT_SECTION = SECURITY_SECTION_IDS[0]

export const SITE_SECTION_IDS = [
  'system-info',
  'notice',
  'header-navigation',
  'sidebar-modules',
] as const
export const SITE_DEFAULT_SECTION = SITE_SECTION_IDS[0]
