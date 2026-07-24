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
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import {
  buildHealthTrendData,
  getHealthTrendXDomain,
  getHealthTrendPointColor,
  getHealthTrendYDomain,
  HEALTH_TREND_BATCH_COLORS,
  HEALTH_TREND_SIGNAL_STYLES,
} from './health-trend-data'
import { buildHealthTrendSpec } from './health-trend-spec'

describe('model health trend signals', () => {
  it('keeps semantic colors stable when observed traffic appears first', () => {
    const data = buildHealthTrendData(
      [
        {
          ts: 1,
          probe_availability: null,
          probe_status: 'idle',
          observed_success_rate: 91,
          observed_request_count: 20,
        },
        {
          ts: 2,
          probe_availability: 99,
          probe_status: 'healthy',
          observed_success_rate: 92,
          observed_request_count: 22,
        },
      ],
      { probe: 'Active probe', observed: 'Channel attempt success rate' },
      (timestamp) => String(timestamp)
    )

    assert.deepEqual(
      data.probe.map((datum) => [datum.signal, datum.signalLabel, datum.value]),
      [
        ['probe', 'Active probe', null],
        ['probe', 'Active probe', 99],
      ]
    )
    assert.deepEqual(
      data.observed.map((datum) => [
        datum.signal,
        datum.signalLabel,
        datum.value,
      ]),
      [
        ['observed', 'Channel attempt success rate', 91],
        ['observed', 'Channel attempt success rate', 92],
      ]
    )
    assert.deepEqual(HEALTH_TREND_SIGNAL_STYLES.probe, {
      color: '#10b981',
      lineDash: [],
      curveType: 'monotone',
    })
    assert.deepEqual(HEALTH_TREND_SIGNAL_STYLES.observed, {
      color: '#3b82f6',
      lineDash: [8, 6],
      curveType: 'monotone',
    })
    assert.equal(data.probe[1].isLast, true)
    assert.equal(data.observed[1].isLast, true)
  })

  it('uses a conservative percentage domain while revealing healthy-range changes', () => {
    const data = buildHealthTrendData(
      [
        {
          ts: 1,
          probe_availability: 99.7,
          probe_status: 'healthy',
          observed_success_rate: 98.9,
          observed_request_count: 50,
        },
        {
          ts: 2,
          probe_availability: 93.5,
          probe_status: 'fluctuating',
          observed_success_rate: 79.3,
          observed_request_count: 54,
        },
      ],
      { probe: 'Active probe', observed: 'Channel attempt success rate' }
    )

    assert.deepEqual(getHealthTrendYDomain(data), [77, 100])
    assert.deepEqual(
      getHealthTrendYDomain({
        probe: [{ ...data.probe[0], value: 99.9 }],
        observed: [],
      }),
      [88, 100]
    )
    assert.deepEqual(
      getHealthTrendYDomain({ probe: [], observed: [] }),
      [0, 100]
    )
  })

  it('clamps invalid percentages and only marks the latest valid sample', () => {
    const data = buildHealthTrendData(
      [
        {
          ts: 1,
          probe_availability: 104,
          probe_status: 'healthy',
          observed_success_rate: -3,
          observed_request_count: 10,
        },
        {
          ts: 2,
          probe_availability: Number.NaN,
          probe_status: 'idle',
          observed_success_rate: 97,
          observed_request_count: 12,
        },
      ],
      { probe: 'Active probe', observed: 'Observed traffic' }
    )

    assert.deepEqual(
      data.probe.map((datum) => [datum.value, datum.isLast]),
      [
        [100, true],
        [null, false],
      ]
    )
    assert.deepEqual(
      data.observed.map((datum) => [datum.value, datum.isLast]),
      [
        [0, false],
        [97, true],
      ]
    )
  })

  it('keeps dense mixed buckets ordered, merged, and separated by signal', () => {
    const densePoints = Array.from({ length: 144 }, (_, index) => {
      let probeAvailability: number | null = null
      let observedSuccessRate: number | null = null
      if (index !== 37) {
        probeAvailability = index % 2 === 0 ? 0 : 100
        observedSuccessRate = 94 + (index % 5)
      }
      return {
        ts: 1_000 + index * 300,
        probe_availability: probeAvailability,
        probe_status: 'healthy' as const,
        observed_success_rate: observedSuccessRate,
        observed_request_count: 30 + index,
      }
    })
    const points = [
      ...densePoints.filter((_, index) => index % 2 === 0).reverse(),
      {
        ...densePoints[12],
        probe_availability: null,
        observed_success_rate: 42,
      },
      {
        ...densePoints[12],
        probe_availability: 100,
        observed_success_rate: null,
      },
      ...densePoints.filter((_, index) => index % 2 === 1).reverse(),
    ]

    const data = buildHealthTrendData(
      points,
      { probe: 'Active probe', observed: 'Observed traffic' },
      (timestamp) => String(timestamp)
    )
    const expectedTimestamps = densePoints.map((point) => point.ts)

    assert.deepEqual(
      data.probe.map((datum) => datum.ts),
      expectedTimestamps.filter((timestamp) => timestamp !== densePoints[37].ts)
    )
    assert.deepEqual(
      data.observed.map((datum) => datum.ts),
      expectedTimestamps
    )
    assert.equal(new Set(data.probe.map((datum) => datum.ts)).size, 143)
    assert.equal(new Set(data.observed.map((datum) => datum.ts)).size, 144)
    assert.equal(data.probe[12].value, 100)
    assert.equal(data.observed[12].value, 42)
    assert.equal(data.observed[37].value, null)
    assert.deepEqual(
      data.probe.slice(0, 8).map((datum) => datum.value),
      [0, 100, 0, 100, 0, 100, 0, 100]
    )
    assert.equal(data.probe.at(-1)?.isLast, true)
    assert.equal(data.observed.at(-1)?.isLast, true)
  })

  it('builds independent unsampled lines and preserves long gaps', () => {
    const data = buildHealthTrendData(
      [
        {
          ts: 1,
          probe_availability: 0,
          probe_status: 'unstable',
          observed_success_rate: 98,
          observed_request_count: 30,
        },
        {
          ts: 2,
          probe_availability: null,
          probe_status: 'idle',
          observed_success_rate: null,
          observed_request_count: 0,
        },
        {
          ts: 3,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: 99,
          observed_request_count: 35,
        },
      ],
      { probe: 'Active probe', observed: 'Observed traffic' }
    )
    const spec = buildHealthTrendSpec({
      data,
      yDomain: getHealthTrendYDomain(data),
      endpointStroke: '#fff',
      referenceStroke: '#ccc',
    })
    const lineSeries = spec.series.filter((series) => series.type === 'line')

    assert.deepEqual(
      spec.data.map((seriesData) => seriesData.id),
      ['health-probe', 'health-observed']
    )
    assert.deepEqual(
      lineSeries.map((series) => series.dataId),
      ['health-probe', 'health-observed']
    )
    assert.deepEqual(
      lineSeries.map((series) => series.invalidType),
      ['break', 'break']
    )
    assert.deepEqual(
      lineSeries.map((series) => series.line.style.curveType),
      ['monotone', 'monotone']
    )
    assert.equal(spec.series.length, 2)
    for (const series of spec.series) {
      assert.equal('sampling' in series, false)
      assert.equal('seriesField' in series, false)
      assert.equal('area' in series, false)
    }

    const gapData = buildHealthTrendData(
      [
        {
          ts: 300,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: 99,
          observed_request_count: 30,
        },
        {
          ts: 1500,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: 98,
          observed_request_count: 30,
        },
      ],
      { probe: 'Active probe', observed: 'Observed traffic' },
      (timestamp) => String(timestamp),
      300
    )
    assert.deepEqual(
      gapData.probe.map((datum) => [datum.ts, datum.value]),
      [
        [300, 100],
        [600, null],
        [900, null],
        [1200, null],
        [1500, 100],
      ]
    )
  })

  it('bridges one or two synthesized missing probe buckets', () => {
    const cases = [
      {
        endTs: 900,
        expectedObserved: [
          [300, 99],
          [600, null],
          [900, 97],
        ],
      },
      {
        endTs: 1200,
        expectedObserved: [
          [300, 99],
          [600, null],
          [900, null],
          [1200, 97],
        ],
      },
    ]

    for (const testCase of cases) {
      const data = buildHealthTrendData(
        [
          {
            ts: 300,
            probe_availability: 100,
            probe_status: 'healthy',
            observed_success_rate: 99,
            observed_request_count: 30,
          },
          {
            ts: testCase.endTs,
            probe_availability: 98,
            probe_status: 'healthy',
            observed_success_rate: 97,
            observed_request_count: 30,
          },
        ],
        { probe: 'Active probe', observed: 'Observed traffic' },
        (timestamp) => String(timestamp),
        300
      )

      assert.deepEqual(
        data.probe.map((datum) => [datum.ts, datum.value]),
        [
          [300, 100],
          [testCase.endTs, 98],
        ]
      )
      assert.deepEqual(
        data.observed.map((datum) => [datum.ts, datum.value]),
        testCase.expectedObserved
      )
    }
  })

  it('bridges one or two bounded probe gaps while observed gaps remain', () => {
    const data = buildHealthTrendData(
      [
        {
          ts: 0,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: 99,
          observed_request_count: 30,
        },
        {
          ts: 300,
          probe_availability: null,
          probe_status: 'idle',
          observed_success_rate: null,
          observed_request_count: 0,
        },
        {
          ts: 600,
          probe_availability: 98,
          probe_status: 'healthy',
          observed_success_rate: 97,
          observed_request_count: 30,
        },
        {
          ts: 900,
          probe_availability: null,
          probe_status: 'idle',
          observed_success_rate: null,
          observed_request_count: 0,
        },
        {
          ts: 1200,
          probe_availability: null,
          probe_status: 'idle',
          observed_success_rate: null,
          observed_request_count: 0,
        },
        {
          ts: 1500,
          probe_availability: 96,
          probe_status: 'fluctuating',
          observed_success_rate: 95,
          observed_request_count: 30,
        },
      ],
      { probe: 'Active probe', observed: 'Observed traffic' },
      (timestamp) => String(timestamp),
      300
    )

    assert.deepEqual(
      data.probe.map((datum) => [datum.ts, datum.value]),
      [
        [0, 100],
        [600, 98],
        [1500, 96],
      ]
    )
    assert.deepEqual(
      data.observed.map((datum) => [datum.ts, datum.value]),
      [
        [0, 99],
        [300, null],
        [600, 97],
        [900, null],
        [1200, null],
        [1500, 95],
      ]
    )
  })

  it('keeps three consecutive probe gaps as a visible break', () => {
    const data = buildHealthTrendData(
      [
        {
          ts: 0,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: 99,
          observed_request_count: 30,
        },
        ...[300, 600, 900].map((ts) => ({
          ts,
          probe_availability: null,
          probe_status: 'idle' as const,
          observed_success_rate: null,
          observed_request_count: 0,
        })),
        {
          ts: 1200,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: 99,
          observed_request_count: 30,
        },
      ],
      { probe: 'Active probe', observed: 'Observed traffic' },
      (timestamp) => String(timestamp),
      300
    )

    assert.deepEqual(
      data.probe.map((datum) => [datum.ts, datum.value]),
      [
        [0, 100],
        [300, null],
        [600, null],
        [900, null],
        [1200, 100],
      ]
    )
  })

  it('counts synthesized and explicit probe gaps in the same run', () => {
    const data = buildHealthTrendData(
      [
        {
          ts: 0,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: 99,
          observed_request_count: 30,
        },
        {
          ts: 600,
          probe_availability: null,
          probe_status: 'idle',
          observed_success_rate: null,
          observed_request_count: 0,
        },
        {
          ts: 900,
          probe_availability: null,
          probe_status: 'idle',
          observed_success_rate: null,
          observed_request_count: 0,
        },
        {
          ts: 1200,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: 99,
          observed_request_count: 30,
        },
      ],
      { probe: 'Active probe', observed: 'Observed traffic' },
      (timestamp) => String(timestamp),
      300
    )

    assert.deepEqual(
      data.probe.map((datum) => [datum.ts, datum.value]),
      [
        [0, 100],
        [300, null],
        [600, null],
        [900, null],
        [1200, 100],
      ]
    )
  })

  it('preserves each five-minute observed bucket without crossing gaps', () => {
    const data = buildHealthTrendData(
      [
        {
          ts: 0,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: 100,
          observed_request_count: 10,
        },
        {
          ts: 300,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: 80,
          observed_request_count: 30,
        },
        {
          ts: 600,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: 90,
          observed_request_count: 60,
        },
        {
          ts: 900,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: null,
          observed_request_count: 0,
        },
        {
          ts: 1200,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: 70,
          observed_request_count: 5,
        },
      ],
      { probe: 'Active probe', observed: 'Observed traffic' },
      (timestamp) => String(timestamp),
      300
    )

    assert.deepEqual(
      data.observed.map((datum) => [datum.ts, datum.value]),
      [
        [0, 100],
        [300, 80],
        [600, 90],
        [900, null],
        [1200, 70],
      ]
    )
  })

  it('uses the formatted dimension tooltip for both health signals', () => {
    const data = buildHealthTrendData(
      [
        {
          ts: 1_700_000_000,
          probe_availability: 99.126,
          probe_status: 'healthy',
          observed_success_rate: 97.8,
          observed_request_count: 30,
        },
      ],
      { probe: 'Active probe', observed: 'Channel attempt success rate' },
      (timestamp) => `formatted-${timestamp}`
    )
    const spec = buildHealthTrendSpec({
      data,
      yDomain: getHealthTrendYDomain(data),
      endpointStroke: '#fff',
      referenceStroke: '#ccc',
    })
    const titleValue = spec.tooltip.dimension.title.value
    const content = spec.tooltip.dimension.content[0]

    assert.equal(spec.tooltip.activeType, 'dimension')
    assert.equal(titleValue(data.probe[0]), 'formatted-1700000000')
    assert.equal(content.key(data.probe[0]), 'Active probe')
    assert.equal(content.value(data.probe[0]), '99.13%')
    assert.equal(content.key(data.observed[0]), 'Channel attempt success rate')
    assert.equal(content.value(data.observed[0]), '97.80%')
    assert.equal(content.value({ ...data.observed[0], value: null }), '-')
    assert.deepEqual(
      spec.series.map((series) => [
        series.id,
        series.dataKey,
        series.animation,
      ]),
      [
        ['health-probe-series', 'ts', false],
        ['health-observed-series', 'ts', false],
      ]
    )
  })

  it('shows batch attempt counts for probe tooltips and keeps old responses compatible', () => {
    const data = buildHealthTrendData(
      [
        {
          ts: 1_700_000_000,
          probe_availability: 66.67,
          probe_status: 'fluctuating',
          probe_run_count: 1,
          probe_success_count: 2,
          probe_attempt_count: 3,
          observed_success_rate: null,
          observed_request_count: 0,
        },
        {
          ts: 1_700_000_300,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: null,
          observed_request_count: 0,
        },
        {
          ts: 1_700_000_600,
          probe_availability: 33.33,
          probe_status: 'unstable',
          success_count: 1,
          attempt_count: 3,
          observed_success_rate: null,
          observed_request_count: 0,
        },
        {
          ts: 1_700_000_900,
          probe_availability: 70,
          probe_status: 'fluctuating',
          probe_run_count: 4,
          probe_success_count: 3,
          probe_attempt_count: 5,
          observed_success_rate: null,
          observed_request_count: 0,
        },
      ],
      { probe: 'Active probe', observed: 'Observed traffic' }
    )
    const spec = buildHealthTrendSpec({
      data,
      yDomain: getHealthTrendYDomain(data),
      endpointStroke: '#fff',
      referenceStroke: '#ccc',
      formatProbeTooltip: (success, attempts, percentage, runCount) => {
        if (runCount != null && runCount > 1) {
          return `${runCount} runs - ${success}/${attempts} attempts successful - ${percentage.toFixed(2)}% equal-weighted per run`
        }
        return `${success}/${attempts} successful - ${percentage.toFixed(2)}%`
      },
    })
    const content = spec.tooltip.dimension.content[0]

    assert.equal(content.value(data.probe[0]), '2/3 successful - 66.67%')
    assert.equal(content.value(data.probe[1]), '100.00%')
    assert.equal(content.value(data.probe[2]), '1/3 successful - 33.33%')
    assert.equal(
      content.value(data.probe[3]),
      '4 runs - 3/5 attempts successful - 70.00% equal-weighted per run'
    )
    assert.equal(data.probe[3].value, 70)
    assert.equal(
      getHealthTrendPointColor(data.probe[0]),
      HEALTH_TREND_BATCH_COLORS.fluctuating
    )
    assert.equal(
      getHealthTrendPointColor({
        ...data.probe[0],
        probeSuccessCount: 0,
      }),
      HEALTH_TREND_BATCH_COLORS.unstable
    )
    assert.equal(
      getHealthTrendPointColor(data.probe[1]),
      HEALTH_TREND_SIGNAL_STYLES.probe.color
    )
    const probeSeries = spec.series[0]
    const probeFill = probeSeries.point.style.fill
    assert.equal(probeSeries.point.style.size(data.probe[0]), 4.5)
    assert.equal(typeof probeFill, 'function')
    if (typeof probeFill === 'function') {
      assert.equal(
        probeFill(data.probe[0]),
        HEALTH_TREND_BATCH_COLORS.fluctuating
      )
    }
    assert.equal(
      content.shapeColor(data.probe[0]),
      HEALTH_TREND_BATCH_COLORS.fluctuating
    )
  })

  it('uses one continuous timestamp domain for both signals', () => {
    const data = buildHealthTrendData(
      [
        {
          ts: 300,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: 98,
          observed_request_count: 30,
        },
        {
          ts: 600,
          probe_availability: null,
          probe_status: 'idle',
          observed_success_rate: 97,
          observed_request_count: 30,
        },
        {
          ts: 900,
          probe_availability: 100,
          probe_status: 'healthy',
          observed_success_rate: 99,
          observed_request_count: 30,
        },
      ],
      { probe: 'Active probe', observed: 'Observed traffic' }
    )

    assert.deepEqual(
      data.probe.map((datum) => datum.ts),
      [300, 900]
    )
    assert.deepEqual(
      data.observed.map((datum) => datum.ts),
      [300, 600, 900]
    )
    assert.deepEqual(getHealthTrendXDomain(data), [300, 900])
    const spec = buildHealthTrendSpec({
      data,
      yDomain: getHealthTrendYDomain(data),
      endpointStroke: '#fff',
      referenceStroke: '#ccc',
    })
    assert.deepEqual(spec.axes[0], {
      orient: 'bottom',
      type: 'linear',
      min: 300,
      max: 900,
      zero: false,
      nice: false,
      visible: false,
    })
    assert.deepEqual(
      getHealthTrendXDomain({
        probe: [{ ...data.probe[0], ts: 600 }],
        observed: [],
      }),
      [599, 601]
    )
    assert.deepEqual(getHealthTrendXDomain({ probe: [], observed: [] }), [0, 1])
  })
})
