import { describe, expect, it } from 'vitest'

import type { MetricSample } from '@/api/control'

import {
  buildHistoryCounterRates,
  buildHistoryTimeline,
  buildTemperatureIndex,
  counterRate,
  historyValue,
  isHistoryCounter,
  mergeMetricSampleWindow,
  projectHistoryGauge,
  projectHistoryValues,
  projectTemperatureHistory,
} from './series'

function sample(collectedAt: string, cpuPercent: number): MetricSample {
  return { collectedAt, cpuPercent, memoryPercent: 20, load1: 1, diskPercent: 40, networkReceiveBytes: 1, networkTransmitBytes: 1 }
}

const baseTimestamp = Date.parse('2026-08-27T00:00:00Z')

function minuteSample(minute: number, overrides: Partial<MetricSample> = {}): MetricSample {
  return { ...sample(new Date(baseTimestamp + minute * 60_000).toISOString(), minute), ...overrides }
}

function validTimeline(samples: MetricSample[]) {
  const timeline = buildHistoryTimeline(samples)
  if (!timeline.valid) throw new Error(`Unexpected invalid timeline at ${timeline.sampleIndex}`)
  return timeline
}

describe('historyValue', () => {
  it.each([0, 1, -5, 12.5, Number.MAX_VALUE])('preserves the finite gauge value %s', (value) => {
    expect(historyValue(value)).toBe(value)
  })

  it.each([undefined, null, Number.NaN, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY, '0', false, {}])('treats %s as unavailable without coercing it to zero', (value) => {
    expect(historyValue(value)).toBeNull()
  })
})

describe('counterRate', () => {
  it('calculates per-second rates for adjacent minutes with the requested unit', () => {
    expect(counterRate(1_000, 62_440, baseTimestamp, baseTimestamp + 60_000, 1024)).toBe(1)
    expect(counterRate(0, 62_914_560, '2026-08-27T00:00:00Z', '2026-08-27T00:01:00Z', 1024 ** 2)).toBe(1)
    expect(counterRate(100, 160, baseTimestamp, baseTimestamp + 60_000, 1)).toBe(1)
    expect(counterRate(100, 160, baseTimestamp, baseTimestamp + 30_000, 1)).toBe(2)
  })

  it('preserves a real unchanged zero rate, including counters at the safe integer boundary', () => {
    expect(counterRate(0, 0, 0, 60_000, 1024)).toBe(0)
    expect(counterRate(Number.MAX_SAFE_INTEGER, Number.MAX_SAFE_INTEGER, 0, 60_000, 1)).toBe(0)
    expect(counterRate(Number.MAX_SAFE_INTEGER - 60, Number.MAX_SAFE_INTEGER, 0, 60_000, 1)).toBe(1)
  })

  it.each([
    undefined,
    null,
    Number.NaN,
    Number.POSITIVE_INFINITY,
    Number.NEGATIVE_INFINITY,
    -1,
    1.5,
    Number.MAX_SAFE_INTEGER + 1,
    '100',
    false,
  ])('rejects an invalid counter %s in either direction', (value) => {
    expect(isHistoryCounter(value)).toBe(false)
    expect(counterRate(value, 200, 0, 60_000, 1)).toBeNull()
    expect(counterRate(100, value, 0, 60_000, 1)).toBeNull()
  })

  it.each([
    [0, 0],
    [60_000, 0],
    [0, 60_001],
    [0, 60_000.5],
    ['not a date', '2026-08-27T00:01:00Z'],
    ['2026-08-27T00:00:00Z', 'not a date'],
    [undefined, 60_000],
    [0, null],
    [0, Number.NaN],
    [Number.POSITIVE_INFINITY, 60_000],
    [0, Number.NEGATIVE_INFINITY],
    [0, 8.64e15 + 1],
  ])('rejects unavailable, unordered, or overlong timestamps %s → %s', (previousAt, currentAt) => {
    expect(counterRate(100, 200, previousAt, currentAt, 1)).toBeNull()
  })

  it('does not turn a counter reset into an idle zero', () => {
    expect(counterRate(200, 100, 0, 60_000, 1)).toBeNull()
  })

  it.each([0, -1, Number.NaN, Number.POSITIVE_INFINITY])('rejects an invalid divisor or gap limit %s', (value) => {
    expect(counterRate(100, 200, 0, 60_000, value)).toBeNull()
    expect(counterRate(100, 200, 0, 60_000, 1, value)).toBeNull()
  })

  it('rejects a non-finite result and supports an explicit smaller gap threshold', () => {
    expect(counterRate(0, Number.MAX_SAFE_INTEGER, 0, 1, Number.MIN_VALUE)).toBeNull()
    expect(counterRate(100, 200, 0, 60_000, 1, 30_000)).toBeNull()
    expect(counterRate(100, 160, 0, 30_000, 1, 30_000)).toBe(2)
  })
})

describe('buildHistoryTimeline and projection', () => {
  it('returns a valid empty timeline and preserves a single saved sample', () => {
    expect(buildHistoryTimeline([])).toEqual({ valid: true, timestamps: [], sourceIndices: [], sampleTimestamps: [] })
    expect(buildHistoryTimeline([minuteSample(0)])).toEqual({
      valid: true, timestamps: [baseTimestamp], sourceIndices: [0], sampleTimestamps: [baseTimestamp],
    })
  })

  it('inserts one midpoint per long gap without changing saved samples or their count', () => {
    const samples = [0, 1, 60, 61].map((minute) => minuteSample(minute))
    const original = structuredClone(samples)
    const timeline = validTimeline(samples)

    expect(timeline.timestamps).toEqual([0, 1, 30.5, 60, 61].map((minute) => baseTimestamp + minute * 60_000))
    expect(timeline.sourceIndices).toEqual([0, 1, null, 2, 3])
    expect(timeline.sampleTimestamps).toEqual([0, 1, 60, 61].map((minute) => baseTimestamp + minute * 60_000))
    expect(projectHistoryGauge(timeline, samples, (item) => item.cpuPercent)).toEqual([0, 1, null, 60, 61])
    expect(projectHistoryValues(timeline, [0, 1, 60, 61])).toEqual([0, 1, null, 60, 61])
    expect(samples).toEqual(original)
    expect(samples).toHaveLength(4)
  })

  it('uses the exact 60,000 ms boundary and never creates a minute-sized gap matrix', () => {
    const at = (offset: number) => sample(new Date(baseTimestamp + offset).toISOString(), 1)
    const timeline = validTimeline([at(0), at(60_000), at(120_001), at(7 * 24 * 60 * 60_000)])
    expect(timeline.sourceIndices).toEqual([0, 1, null, 2, null, 3])
    expect(timeline.timestamps[2]).toBe(baseTimestamp + 90_000.5)
  })

  it.each([
    { samples: [sample('invalid', 1)], reason: 'invalid-timestamp', sampleIndex: 0 },
    { samples: [minuteSample(0), sample('', 1)], reason: 'invalid-timestamp', sampleIndex: 1 },
    { samples: [minuteSample(0), minuteSample(0)], reason: 'non-increasing-timestamp', sampleIndex: 1 },
    { samples: [minuteSample(1), minuteSample(0)], reason: 'non-increasing-timestamp', sampleIndex: 1 },
    { samples: [minuteSample(0), minuteSample(2), minuteSample(1)], reason: 'non-increasing-timestamp', sampleIndex: 2 },
  ])('reports $reason at $sampleIndex without sorting or discarding input', ({ samples, reason, sampleIndex }) => {
    const original = structuredClone(samples)
    expect(buildHistoryTimeline(samples)).toEqual({ valid: false, reason, sampleIndex })
    expect(samples).toEqual(original)
  })

  it('sanitizes non-finite/missing gauges, preserves real zero, and does not fill missing values', () => {
    const samples = [minuteSample(0), minuteSample(1), minuteSample(5), minuteSample(6), minuteSample(7)]
    const timeline = validTimeline(samples)
    expect(projectHistoryValues(timeline, [0, Number.NaN, Number.POSITIVE_INFINITY, null])).toEqual([0, null, null, null, null, null])
    expect(projectHistoryGauge(timeline, samples, (item, index) => index === 2 ? undefined : item.cpuPercent)).toEqual([0, 1, null, null, 6, 7])
    expect(projectHistoryGauge(timeline, samples.slice(0, 1), (item) => item.cpuPercent)).toEqual([0, null, null, null, null, null])
  })
})

describe('buildHistoryCounterRates', () => {
  it('warms up at the query boundary and each long gap, then resumes from adjacent saved samples', () => {
    const samples = [0, 1, 60, 61].map((minute) => minuteSample(minute, { networkReceiveBytes: minute * 60 }))
    expect(buildHistoryCounterRates(samples, validTimeline(samples), 'networkReceiveBytes', 1)).toEqual([null, 1, null, null, 1])
    expect(buildHistoryCounterRates(samples.slice(1), validTimeline(samples.slice(1)), 'networkReceiveBytes', 1)).toEqual([null, null, null, 1])
  })

  it('keeps the two directions independent and recovers immediately after a reset establishes the baseline', () => {
    const receive = [100, 160, 0, 60]
    const transmit = [100, 100, 160, 220]
    const samples = receive.map((value, index) => minuteSample(index, {
      networkReceiveBytes: value,
      networkTransmitBytes: transmit[index]!,
    }))
    const timeline = validTimeline(samples)
    expect(buildHistoryCounterRates(samples, timeline, 'networkReceiveBytes', 1)).toEqual([null, 1, null, 1])
    expect(buildHistoryCounterRates(samples, timeline, 'networkTransmitBytes', 1)).toEqual([null, 0, 1, 1])
  })

  it('does not skip over a missing counter to manufacture a rate', () => {
    const samples = [100, undefined, 160, 220].map((value, index) => minuteSample(index, { diskReadBytes: value }))
    expect(buildHistoryCounterRates(samples, validTimeline(samples), 'diskReadBytes', 1)).toEqual([null, null, null, 1])
    expect(buildHistoryCounterRates(samples, validTimeline(samples), 'diskWriteBytes', 1)).toEqual([null, null, null, null])
  })
})

describe('temperature history index', () => {
  it('keeps each raw sensor independent through missing readings, disappearance, and reappearance', () => {
    const samples = [
      minuteSample(0, { temperatures: [{ name: 'B', temperatureCelsius: 20 }, { name: 'A', temperatureCelsius: 0 }] }),
      minuteSample(1, { temperatures: [{ name: 'A', temperatureCelsius: 1 }] }),
      minuteSample(2, { temperatures: [{ name: 'B', temperatureCelsius: 22 }] }),
      minuteSample(3, { temperatures: [{ name: 'B', temperatureCelsius: 23 }, { name: 'A', temperatureCelsius: 3 }] }),
      minuteSample(4, { temperatures: [] }),
      minuteSample(7, { temperatures: [{ name: 'A', temperatureCelsius: 7 }] }),
    ]
    const original = structuredClone(samples)
    const timeline = validTimeline(samples)
    const index = buildTemperatureIndex(samples)

    expect(index.names).toEqual(['A', 'B'])
    expect(index.readingCount).toBe(7)
    expect(index.hasData).toBe(true)
    expect(index.readings.get('A')?.size).toBe(4)
    expect(index.readings.get('B')?.size).toBe(3)
    expect(projectTemperatureHistory(timeline, index, 'A')).toEqual([0, 1, null, 3, null, null, 7])
    expect(projectTemperatureHistory(timeline, index, 'B')).toEqual([20, null, 22, 23, null, null, null])
    expect(projectTemperatureHistory(timeline, index, 'unknown')).toEqual([null, null, null, null, null, null, null])
    expect(samples).toEqual(original)
  })

  it('keeps raw sensor names unique and handles omitted, empty, and invalid readings as unavailable', () => {
    const samples = [
      minuteSample(0),
      minuteSample(1, { temperatures: [] }),
      minuteSample(2, { temperatures: [
        { name: 'CPU', temperatureCelsius: Number.NaN },
        { name: 'cpu', temperatureCelsius: Number.POSITIVE_INFINITY },
        { name: '<sensor&1>', temperatureCelsius: Number.NEGATIVE_INFINITY },
      ] }),
    ]
    const index = buildTemperatureIndex(samples)
    expect(index.names).toEqual(['<sensor&1>', 'CPU', 'cpu'])
    expect(index.hasData).toBe(false)
    expect(index.readingCount).toBe(3)
    for (const name of index.names) expect(projectTemperatureHistory(validTimeline(samples), index, name)).toEqual([null, null, null])
    expect(buildTemperatureIndex([])).toEqual({ names: [], readings: new Map(), readingCount: 0, hasData: false })
  })

  it('retains all 65,536 readings across 10,081 samples while allocating only sparse sensor entries', () => {
    const samples = Array.from({ length: 10_081 }, (_, index) => minuteSample(index, { temperatures: [] }))
    for (let index = 0; index < 65_536; index++) {
      samples[index % samples.length]!.temperatures!.push({ name: `sensor-${String(index).padStart(5, '0')}`, temperatureCelsius: index % 100 })
    }
    const timeline = validTimeline(samples)
    const index = buildTemperatureIndex(samples)
    const selected = index.names.slice(0, 8)
    const projected = selected.map((name) => projectTemperatureHistory(timeline, index, name))

    expect(timeline.sampleTimestamps).toHaveLength(10_081)
    expect(timeline.timestamps).toHaveLength(10_081)
    expect(index.readingCount).toBe(65_536)
    expect(index.names).toHaveLength(65_536)
    expect(index.readings.size).toBe(65_536)
    expect([...index.readings.values()].every((readings) => readings.size === 1)).toBe(true)
    expect(projected).toHaveLength(8)
    expect(projected.every((values) => values.length === 10_081)).toBe(true)
    expect(projected.every((values) => values.filter((value) => value !== null).length === 1)).toBe(true)
    expect(samples.reduce((count, item) => count + item.temperatures!.length, 0)).toBe(65_536)
  })

  it('bounds the displayed slots to 2N−1 even when every saved sample is separated by a gap', () => {
    const samples = Array.from({ length: 10_081 }, (_, index) => minuteSample(index * 2))
    const timeline = validTimeline(samples)
    expect(timeline.timestamps).toHaveLength(20_161)
    expect(timeline.sourceIndices.filter((sourceIndex) => sourceIndex === null)).toHaveLength(10_080)
    expect(timeline.sampleTimestamps).toHaveLength(10_081)
    expect(projectHistoryGauge(timeline, samples, (item) => item.cpuPercent)).toHaveLength(20_161)
  })
})

describe('mergeMetricSampleWindow', () => {
  it('sorts samples, replaces duplicate timestamps, and trims outside the active range', () => {
    const result = mergeMetricSampleWindow([
      sample('2026-08-27T00:00:02Z', 2),
      sample('2026-08-27T00:00:00Z', 0),
      sample('2026-08-26T23:59:59Z', -1),
    ], sample('2026-08-27T00:00:02Z', 22), Date.parse('2026-08-27T00:00:00Z'), Date.parse('2026-08-27T00:00:03Z'))

    expect(result.map((item) => item.cpuPercent)).toEqual([0, 22])
  })
})
