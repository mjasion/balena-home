package otel

import (
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// CustomSampler implements a sampler that uses different sampling rates
// based on span attributes or name
type CustomSampler struct {
	defaultSampler sdktrace.Sampler
	metricsSampler sdktrace.Sampler
}

// NewCustomSampler creates a new custom sampler with different rates
// for default operations and metrics operations
func NewCustomSampler(defaultRate, metricsRate float64) sdktrace.Sampler {
	var defaultSampler sdktrace.Sampler
	if defaultRate >= 1.0 {
		defaultSampler = sdktrace.AlwaysSample()
	} else if defaultRate <= 0.0 {
		defaultSampler = sdktrace.NeverSample()
	} else {
		defaultSampler = sdktrace.TraceIDRatioBased(defaultRate)
	}

	var metricsSampler sdktrace.Sampler
	if metricsRate >= 1.0 {
		metricsSampler = sdktrace.AlwaysSample()
	} else if metricsRate <= 0.0 {
		metricsSampler = sdktrace.NeverSample()
	} else {
		metricsSampler = sdktrace.TraceIDRatioBased(metricsRate)
	}

	return &CustomSampler{
		defaultSampler: defaultSampler,
		metricsSampler: metricsSampler,
	}
}

// ShouldSample implements the Sampler interface
func (s *CustomSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	// Check if this is a metrics push span
	if p.Name == "metrics.push" {
		return s.metricsSampler.ShouldSample(p)
	}

	// Use default sampler for all other spans
	return s.defaultSampler.ShouldSample(p)
}

// Description implements the Sampler interface
func (s *CustomSampler) Description() string {
	return "CustomSampler{default=" + s.defaultSampler.Description() + ", metrics=" + s.metricsSampler.Description() + "}"
}
