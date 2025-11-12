package otel

import (
	"strings"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// metricsSampler is a simple sampler that uses different sampling rates for metrics operations
type metricsSampler struct {
	metricsSampler sdktrace.Sampler
	defaultSampler sdktrace.Sampler
}

// NewMetricsSampler creates a sampler with separate rates for metrics vs other operations
func NewMetricsSampler(defaultSamplingRate, metricsSamplingRate float64) sdktrace.Sampler {
	return &metricsSampler{
		metricsSampler: createSampler(metricsSamplingRate),
		defaultSampler: createSampler(defaultSamplingRate),
	}
}

// createSampler creates the appropriate sampler based on sampling rate
func createSampler(rate float64) sdktrace.Sampler {
	if rate >= 1.0 {
		return sdktrace.AlwaysSample()
	} else if rate <= 0.0 {
		return sdktrace.NeverSample()
	}
	return sdktrace.TraceIDRatioBased(rate)
}

// ShouldSample implements the Sampler interface
func (s *metricsSampler) ShouldSample(parameters sdktrace.SamplingParameters) sdktrace.SamplingResult {
	// Check if this is a metrics operation (span name starts with "metrics.")
	if strings.HasPrefix(parameters.Name, "metrics.") {
		return s.metricsSampler.ShouldSample(parameters)
	}
	return s.defaultSampler.ShouldSample(parameters)
}

// Description implements the Sampler interface
func (s *metricsSampler) Description() string {
	return "MetricsSampler"
}

// ParentBasedMetricsSampler creates a parent-based sampler with separate metrics sampling
// This ensures that if a parent span is sampled, all child spans are also sampled,
// but root spans follow the metrics vs default sampling rules
func ParentBasedMetricsSampler(defaultSamplingRate, metricsSamplingRate float64) sdktrace.Sampler {
	root := NewMetricsSampler(defaultSamplingRate, metricsSamplingRate)
	return sdktrace.ParentBased(root,
		sdktrace.WithRemoteParentSampled(sdktrace.AlwaysSample()),
		sdktrace.WithRemoteParentNotSampled(root),
		sdktrace.WithLocalParentSampled(sdktrace.AlwaysSample()),
		sdktrace.WithLocalParentNotSampled(root),
	)
}

