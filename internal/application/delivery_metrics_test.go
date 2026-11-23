// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func collectDelivery(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Aggregation {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	out := map[string]metricdata.Aggregation{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m.Data
		}
	}
	return out
}

func sumFor(t *testing.T, data metricdata.Aggregation, attrs ...attribute.KeyValue) int64 {
	t.Helper()
	sum, ok := data.(metricdata.Sum[int64])
	require.True(t, ok)
	want := attribute.NewSet(attrs...)
	for _, dp := range sum.DataPoints {
		if dp.Attributes.Equals(&want) {
			return dp.Value
		}
	}
	return 0
}

func TestDeliveryMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	index, writer := &fakeIndex{}, &fakeFGAWriter{}
	h := newTestDeliveryHandler(index, writer, nil)
	h.metrics = newDeliveryMetrics(provider)

	require.Equal(t, "OK", deliver(t, h, deliverySubject, indexRequest(t, withFGA)))
	writer.err = errors.New("openfga unavailable")
	require.Equal(t, "ERROR: transient: fga: openfga unavailable", deliver(t, h, deliverySubject, indexRequest(t, withFGA)))
	require.Equal(t, "ERROR: rejected: index: invalid request JSON", deliver(t, h, deliverySubject, []byte(`{`)))

	metrics := collectDelivery(t, reader)
	requests := metrics["indexer_delivery.requests"]
	require.EqualValues(t, 1, sumFor(t, requests, attribute.String("result", "ok")))
	require.EqualValues(t, 1, sumFor(t, requests, attribute.String("result", "transient")))
	require.EqualValues(t, 1, sumFor(t, requests, attribute.String("result", "rejected")))

	failures := metrics["indexer_delivery.failures"]
	require.EqualValues(t, 1, sumFor(t, failures, attribute.String("half", "fga"), attribute.String("class", "transient")))
	require.EqualValues(t, 1, sumFor(t, failures, attribute.String("half", "index"), attribute.String("class", "rejected")))

	duration, ok := metrics["indexer_delivery.duration"].(metricdata.Histogram[float64])
	require.True(t, ok)
	counts := map[string]uint64{}
	for _, dp := range duration.DataPoints {
		half, _ := dp.Attributes.Value("half")
		counts[half.AsString()] = dp.Count
	}
	require.Equal(t, map[string]uint64{"index": 2, "fga": 2, "total": 2}, counts)
}
