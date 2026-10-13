import type { MetricSample } from '@/api/control'

export type HistoryValue = number | null

export const HISTORY_MAX_GAP_MS = 60_000

export interface HistoryTimeline {
  valid: true
  timestamps: number[]
  sourceIndices: Array<number | null>
  sampleTimestamps: number[]
}

export type HistoryTimelineResult = HistoryTimeline | {
  valid: false
  reason: 'invalid-timestamp' | 'non-increasing-timestamp'
  sampleIndex: number
}

export type HistoryCounterKey = 'networkReceiveBytes' | 'networkTransmitBytes' | 'diskReadBytes' | 'diskWriteBytes'

export interface TemperatureHistoryIndex {
  names: string[]
  readings: Map<string, Map<number, HistoryValue>>
  readingCount: number
  hasData: boolean
}

export function historyValue(value: unknown): HistoryValue {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

export function isHistoryCounter(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}

function historyTimestamp(value: unknown): HistoryValue {
  if (typeof value === 'string') return historyValue(Date.parse(value))
  if (typeof value === 'number' && Number.isFinite(value) && Math.abs(value) <= 8.64e15) return value
  return null
}

export function counterRate(
  previous: unknown,
  current: unknown,
  previousAt: unknown,
  currentAt: unknown,
  divisor: number,
  maxGapMs = HISTORY_MAX_GAP_MS,
): HistoryValue {
  if (!isHistoryCounter(previous) || !isHistoryCounter(current) || current < previous) return null
  if (!Number.isFinite(divisor) || divisor <= 0 || !Number.isFinite(maxGapMs) || maxGapMs <= 0) return null
  const previousTimestamp = historyTimestamp(previousAt)
  const currentTimestamp = historyTimestamp(currentAt)
  if (previousTimestamp === null || currentTimestamp === null) return null
  const elapsedMs = currentTimestamp - previousTimestamp
  if (elapsedMs <= 0 || elapsedMs > maxGapMs) return null
  return historyValue((current - previous) / (elapsedMs / 1000) / divisor)
}

export function buildHistoryTimeline(samples: readonly Pick<MetricSample, 'collectedAt'>[]): HistoryTimelineResult {
  const timestamps: number[] = []
  const sourceIndices: Array<number | null> = []
  const sampleTimestamps: number[] = []
  for (const [sampleIndex, sample] of samples.entries()) {
    const timestamp = historyTimestamp(sample?.collectedAt)
    if (timestamp === null) return { valid: false, reason: 'invalid-timestamp', sampleIndex }
    const previous = sampleTimestamps.at(-1)
    if (previous !== undefined) {
      if (timestamp <= previous) return { valid: false, reason: 'non-increasing-timestamp', sampleIndex }
      if (timestamp - previous > HISTORY_MAX_GAP_MS) {
        // 一个显示断点足以阻止跨缺口连线，不逐分钟补齐，也不增加真实采样数量。
        timestamps.push(previous + (timestamp - previous) / 2)
        sourceIndices.push(null)
      }
    }
    timestamps.push(timestamp)
    sourceIndices.push(sampleIndex)
    sampleTimestamps.push(timestamp)
  }
  return { valid: true, timestamps, sourceIndices, sampleTimestamps }
}

export function projectHistoryValues(timeline: HistoryTimeline, values: readonly unknown[]): HistoryValue[] {
  return timeline.sourceIndices.map((sourceIndex) => sourceIndex === null ? null : historyValue(values[sourceIndex]))
}

export function projectHistoryGauge<T>(
  timeline: HistoryTimeline,
  samples: readonly T[],
  readValue: (sample: T, sourceIndex: number) => unknown,
): HistoryValue[] {
  return timeline.sourceIndices.map((sourceIndex) => {
    if (sourceIndex === null) return null
    const sample = samples[sourceIndex]
    return sample === undefined ? null : historyValue(readValue(sample, sourceIndex))
  })
}

export function buildHistoryCounterRates(
  samples: readonly MetricSample[],
  timeline: HistoryTimeline,
  key: HistoryCounterKey,
  divisor: number,
): HistoryValue[] {
  return projectHistoryGauge(timeline, samples, (sample, sourceIndex) => counterRate(
    samples[sourceIndex - 1]?.[key],
    sample[key],
    timeline.sampleTimestamps[sourceIndex - 1],
    timeline.sampleTimestamps[sourceIndex],
    divisor,
  ))
}

export function buildTemperatureIndex(samples: readonly MetricSample[]): TemperatureHistoryIndex {
  const readings = new Map<string, Map<number, HistoryValue>>()
  let readingCount = 0
  let hasData = false
  for (const [sampleIndex, sample] of samples.entries()) {
    for (const reading of sample.temperatures ?? []) {
      readingCount++
      if (!reading || typeof reading.name !== 'string' || !reading.name) continue
      let sensor = readings.get(reading.name)
      if (!sensor) {
        sensor = new Map<number, HistoryValue>()
        readings.set(reading.name, sensor)
      }
      const value = historyValue(reading.temperatureCelsius)
      sensor.set(sampleIndex, value)
      if (value !== null) hasData = true
    }
  }
  return { names: [...readings.keys()].sort(), readings, readingCount, hasData }
}

export function projectTemperatureHistory(
  timeline: HistoryTimeline,
  index: TemperatureHistoryIndex,
  rawName: string,
): HistoryValue[] {
  const sensor = index.readings.get(rawName)
  return timeline.sourceIndices.map((sourceIndex) => sourceIndex === null ? null : historyValue(sensor?.get(sourceIndex)))
}

export function mergeMetricSampleWindow(
  samples: MetricSample[],
  incoming: MetricSample,
  lowerBound: number,
  upperBound: number,
): MetricSample[] {
  const merged = new Map<number, MetricSample>()
  for (const sample of [...samples, incoming]) {
    const timestamp = new Date(sample.collectedAt).valueOf()
    if (!Number.isFinite(timestamp) || timestamp < lowerBound || timestamp > upperBound) continue
    merged.set(timestamp, sample)
  }
  return [...merged.entries()]
    .sort(([left], [right]) => left - right)
    .map(([, sample]) => sample)
}
