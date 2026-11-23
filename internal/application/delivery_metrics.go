// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

const deliveryMeterName = "github.com/linuxfoundation/lfx-v2-indexer-service/internal/application"

const halfTotal = "total"

// deliveryMetrics counts request/reply deliveries: requests by reply result,
// duration by half, and failures by half and class.
type deliveryMetrics struct {
	requests metric.Int64Counter
	duration metric.Float64Histogram
	failures metric.Int64Counter
}

// defaultDeliveryMetrics uses the global provider, which forwards to
// whichever provider is registered later.
func defaultDeliveryMetrics() *deliveryMetrics {
	return newDeliveryMetrics(otel.GetMeterProvider())
}

func newDeliveryMetrics(provider metric.MeterProvider) *deliveryMetrics {
	meter := provider.Meter(deliveryMeterName)
	m := &deliveryMetrics{
		requests: deliveryCounter(meter, "indexer_delivery.requests", "{request}", "Request/reply index deliveries by reply result"),
		failures: deliveryCounter(meter, "indexer_delivery.failures", "{request}", "Failed request/reply index deliveries by half and class"),
	}
	duration, err := meter.Float64Histogram("indexer_delivery.duration",
		metric.WithUnit("s"), metric.WithDescription("Request/reply index delivery latency by half"))
	if err != nil {
		m.duration = noop.Float64Histogram{}
	} else {
		m.duration = duration
	}
	return m
}

func deliveryCounter(meter metric.Meter, name, unit, description string) metric.Int64Counter {
	c, err := meter.Int64Counter(name, metric.WithUnit(unit), metric.WithDescription(description))
	if err != nil {
		return noop.Int64Counter{}
	}
	return c
}

// result records one reply: ok, or the class and half of its failure.
func (m *deliveryMetrics) result(ctx context.Context, failure *deliveryFailure) {
	if failure == nil {
		m.requests.Add(ctx, 1, metric.WithAttributes(attribute.String("result", "ok")))
		return
	}
	m.requests.Add(ctx, 1, metric.WithAttributes(attribute.String("result", failure.class)))
	m.failures.Add(ctx, 1, metric.WithAttributes(
		attribute.String("half", failure.half),
		attribute.String("class", failure.class)))
}

func (m *deliveryMetrics) elapsed(ctx context.Context, half string, d time.Duration) {
	m.duration.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String("half", half)))
}
