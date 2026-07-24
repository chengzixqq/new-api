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
import {
  getHealthTrendXDomain,
  getHealthTrendPointColor,
  getHealthTrendPointSize,
  HEALTH_TREND_SIGNAL_STYLES,
  type HealthTrendDatum,
  type HealthTrendSeriesData,
} from './health-trend-data'

export function buildHealthTrendSpec(props: {
  data: HealthTrendSeriesData
  yDomain: [number, number]
  endpointStroke: string
  referenceStroke: string
  formatProbeTooltip?: (
    successCount: number,
    attemptCount: number,
    percentage: number,
    runCount: number | null
  ) => string
}) {
  const probeStyle = HEALTH_TREND_SIGNAL_STYLES.probe
  const observedStyle = HEALTH_TREND_SIGNAL_STYLES.observed
  const xDomain = getHealthTrendXDomain(props.data)

  return {
    type: 'common' as const,
    data: [
      { id: 'health-probe', values: props.data.probe },
      { id: 'health-observed', values: props.data.observed },
    ],
    series: [
      {
        id: 'health-probe-series',
        type: 'line' as const,
        dataId: 'health-probe',
        dataKey: 'ts',
        xField: 'ts',
        yField: 'value',
        animation: false,
        invalidType: 'break' as const,
        point: {
          visible: true,
          style: {
            size: (datum: Partial<HealthTrendDatum>) =>
              getHealthTrendPointSize(datum),
            fill: (datum: Partial<HealthTrendDatum>) =>
              getHealthTrendPointColor(datum),
            stroke: props.endpointStroke,
            lineWidth: 1.5,
          },
        },
        line: {
          visible: true,
          style: {
            stroke: probeStyle.color,
            strokeOpacity: 0.98,
            lineDash: probeStyle.lineDash,
            lineWidth: 2.1,
            curveType: probeStyle.curveType,
            lineCap: 'round' as const,
            lineJoin: 'round' as const,
          },
        },
      },
      {
        id: 'health-observed-series',
        type: 'line' as const,
        dataId: 'health-observed',
        dataKey: 'ts',
        xField: 'ts',
        yField: 'value',
        animation: false,
        invalidType: 'break' as const,
        point: {
          visible: true,
          style: {
            size: (datum: Record<string, unknown>) =>
              datum?.isLast === true ? 5.5 : 0,
            fill: observedStyle.color,
            stroke: props.endpointStroke,
            lineWidth: 1.5,
          },
        },
        line: {
          visible: true,
          style: {
            stroke: observedStyle.color,
            strokeOpacity: 0.92,
            lineDash: observedStyle.lineDash,
            lineWidth: 1.9,
            curveType: observedStyle.curveType,
            lineCap: 'round' as const,
            lineJoin: 'round' as const,
          },
        },
      },
    ],
    legends: { visible: false },
    axes: [
      {
        orient: 'bottom' as const,
        type: 'linear' as const,
        min: xDomain[0],
        max: xDomain[1],
        zero: false,
        nice: false,
        visible: false,
      },
      {
        orient: 'left' as const,
        min: props.yDomain[0],
        max: props.yDomain[1],
        visible: false,
      },
    ],
    markLine: [
      {
        y: 100,
        interactive: false,
        line: {
          style: {
            stroke: props.referenceStroke,
            lineDash: [2, 4],
            lineWidth: 1,
          },
        },
        label: { visible: false },
        startSymbol: { visible: false },
        endSymbol: { visible: false },
      },
      {
        y: (props.yDomain[0] + props.yDomain[1]) / 2,
        interactive: false,
        line: {
          style: {
            stroke: props.referenceStroke,
            strokeOpacity: 0.55,
            lineDash: [1, 5],
            lineWidth: 1,
          },
        },
        label: { visible: false },
        startSymbol: { visible: false },
        endSymbol: { visible: false },
      },
    ],
    padding: { top: 4, right: 4, bottom: 3, left: 0 },
    tooltip: {
      activeType: 'dimension' as const,
      dimension: {
        title: {
          value: (datum?: Partial<HealthTrendDatum>) =>
            String(datum?.timeLabel ?? ''),
        },
        content: [
          {
            key: (datum?: Partial<HealthTrendDatum>) =>
              String(datum?.signalLabel ?? ''),
            value: (datum?: Partial<HealthTrendDatum>) => {
              const value = datum?.value
              if (typeof value !== 'number') return '-'
              if (
                datum?.signal === 'probe' &&
                typeof datum.probeSuccessCount === 'number' &&
                typeof datum.probeAttemptCount === 'number'
              ) {
                return props.formatProbeTooltip
                  ? props.formatProbeTooltip(
                      datum.probeSuccessCount,
                      datum.probeAttemptCount,
                      value,
                      datum.probeRunCount ?? null
                    )
                  : `${datum.probeSuccessCount}/${datum.probeAttemptCount} - ${value.toFixed(2)}%`
              }
              return `${value.toFixed(2)}%`
            },
            hasShape: true,
            shapeType: 'circle',
            shapeColor: (datum?: Partial<HealthTrendDatum>) =>
              getHealthTrendPointColor(datum),
            shapeFill: (datum?: Partial<HealthTrendDatum>) =>
              getHealthTrendPointColor(datum),
            shapeStroke: (datum?: Partial<HealthTrendDatum>) =>
              getHealthTrendPointColor(datum),
          },
        ],
      },
    },
  }
}
