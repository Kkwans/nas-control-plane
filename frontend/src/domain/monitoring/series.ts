import type { MetricSample } from '@/api/control'

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
