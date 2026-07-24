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
export function getDefaultTimeRange(now = new Date()): {
  start: Date
  end: Date
} {
  const start = new Date(now)
  start.setHours(0, 0, 0, 0)
  return {
    start,
    end: new Date(now.getTime() + 3600 * 1000),
  }
}

export function canonicalizeUsageLogTimeRange(
  search: { startTime?: number; endTime?: number },
  now = new Date()
): { startTime: number; endTime: number } {
  const defaults = getDefaultTimeRange(now)
  return {
    startTime:
      typeof search.startTime === 'number' && Number.isFinite(search.startTime)
        ? search.startTime
        : defaults.start.getTime(),
    endTime:
      typeof search.endTime === 'number' && Number.isFinite(search.endTime)
        ? search.endTime
        : defaults.end.getTime(),
  }
}

export function buildTimeRangeParams(
  search: { startTime?: unknown; endTime?: unknown },
  useMilliseconds: boolean,
  now = new Date()
): { start_timestamp: number; end_timestamp: number } {
  const canonical = canonicalizeUsageLogTimeRange(
    {
      startTime:
        typeof search.startTime === 'number' ? search.startTime : undefined,
      endTime: typeof search.endTime === 'number' ? search.endTime : undefined,
    },
    now
  )
  const convert = (timestamp: number) =>
    useMilliseconds ? timestamp : Math.floor(timestamp / 1000)
  return {
    start_timestamp: convert(canonical.startTime),
    end_timestamp: convert(canonical.endTime),
  }
}
