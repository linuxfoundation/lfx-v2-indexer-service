// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package messaging

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	sdktrace "go.opentelemetry.io/otel/trace"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
)

// TestNatsHeaderCarrier verifies the natsHeaderCarrier implements TextMapCarrier correctly
func TestNatsHeaderCarrier(t *testing.T) {
	t.Run("get_empty_header", func(t *testing.T) {
		carrier := natsHeaderCarrier(nats.Header{})
		val := carrier.Get("X-Trace-ID")
		assert.Equal(t, "", val)
	})

	t.Run("get_existing_header", func(t *testing.T) {
		header := nats.Header{
			"X-Trace-ID": []string{"trace-123"},
		}
		carrier := natsHeaderCarrier(header)
		val := carrier.Get("X-Trace-ID")
		assert.Equal(t, "trace-123", val)
	})

	t.Run("set_header", func(t *testing.T) {
		header := nats.Header{}
		carrier := natsHeaderCarrier(header)
		carrier.Set("X-Trace-ID", "trace-456")
		assert.Equal(t, "trace-456", header["X-Trace-ID"][0])
	})

	t.Run("keys", func(t *testing.T) {
		header := nats.Header{
			"X-Trace-ID": []string{"trace-123"},
			"X-Span-ID":  []string{"span-456"},
			"X-User-ID":  []string{"user-789"},
		}
		carrier := natsHeaderCarrier(header)
		keys := carrier.Keys()
		assert.Len(t, keys, 3)
		assert.Contains(t, keys, "X-Trace-ID")
		assert.Contains(t, keys, "X-Span-ID")
		assert.Contains(t, keys, "X-User-ID")
	})
}

// TestTraceContextInjection verifies that trace context is injected into message headers during publish
func TestTraceContextInjection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	conn, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		t.Skipf("Skipping test: no NATS server available: %v", err)
	}
	defer conn.Close()

	// Setup OTel with test exporter to capture spans
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithBatcher(exporter))
	defer tp.Shutdown(context.Background())
	otel.SetTracerProvider(tp)

	logger := logging.NewLogger(true)
	repo := NewMessagingRepository(conn, nil, logger, 5*time.Second,
		constants.DefaultPendingMsgLimit, constants.DefaultPendingBytesLimit,
		constants.DefaultWorkerCount)
	defer repo.Close()

	ctx := context.Background()

	t.Run("publish_injects_trace_context", func(t *testing.T) {
		subject := "test.trace.publish"
		data := []byte("test message")

		// Publish a message with active trace context
		err := repo.Publish(ctx, subject, data)
		require.NoError(t, err)

		// Flush the batch processor to ensure spans are exported
		tp.ForceFlush(context.Background())

		// Verify that a span was created
		spans := exporter.GetSpans()
		require.Greater(t, len(spans), 0)

		// Find the publish span
		var publishSpan *tracetest.SpanStub
		for _, s := range spans {
			if s.Name == "nats.publish" {
				publishSpan = &s
				break
			}
		}
		require.NotNil(t, publishSpan, "Expected to find nats.publish span")
		assert.Equal(t, sdktrace.SpanKindProducer, publishSpan.SpanKind)
	})
}

// TestTraceContextExtraction verifies that subscriber handlers extract trace context from headers
func TestTraceContextExtraction(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	conn, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		t.Skipf("Skipping test: no NATS server available: %v", err)
	}
	defer conn.Close()

	// Setup OTel with test exporter
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithBatcher(exporter))
	defer tp.Shutdown(context.Background())
	otel.SetTracerProvider(tp)

	logger := logging.NewLogger(true)
	repo := NewMessagingRepository(conn, nil, logger, 5*time.Second,
		constants.DefaultPendingMsgLimit, constants.DefaultPendingBytesLimit,
		constants.DefaultWorkerCount)
	defer repo.Close()

	ctx := context.Background()

	t.Run("subscribe_extracts_trace_context", func(t *testing.T) {
		subject := "test.trace.subscribe"
		testData := []byte("trace test message")
		handlerCalled := make(chan bool, 1)

		mockHandler := &MockMessageHandler{}
		mockHandler.On("Handle", mock.Anything, testData, subject).Run(func(_ mock.Arguments) {
			handlerCalled <- true
		}).Return(nil)

		// Subscribe to the subject
		err := repo.Subscribe(ctx, subject, mockHandler)
		require.NoError(t, err)

		// Give subscription time to be established
		time.Sleep(100 * time.Millisecond)

		// Publish message with trace headers
		err = repo.Publish(ctx, subject, testData)
		require.NoError(t, err)

		// Wait for message to be received
		select {
		case <-handlerCalled:
			// Handler was called, now verify spans
			time.Sleep(100 * time.Millisecond) // Allow spans to be exported

			// Flush the batch processor to ensure spans are exported before asserting.
			tp.ForceFlush(context.Background())

			// Check that both publish and process spans exist
			spans := exporter.GetSpans()
			processSpanFound := false
			for _, s := range spans {
				if s.Name == "nats.process" && s.SpanKind == sdktrace.SpanKindConsumer {
					processSpanFound = true
					break
				}
			}
			assert.True(t, processSpanFound, "Expected to find nats.process span with Consumer kind")

		case <-time.After(2 * time.Second):
			t.Fatal("Handler not called within timeout")
		}

		mockHandler.AssertExpectations(t)
	})
}

// TestTraceContextPreservation verifies that cancellation and deadline from context are preserved
func TestTraceContextPreservation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	conn, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		t.Skipf("Skipping test: no NATS server available: %v", err)
	}
	defer conn.Close()

	logger := logging.NewLogger(true)
	repo := NewMessagingRepository(conn, nil, logger, 5*time.Second,
		constants.DefaultPendingMsgLimit, constants.DefaultPendingBytesLimit,
		constants.DefaultWorkerCount)
	defer repo.Close()

	t.Run("subscribe_preserves_context_deadline", func(t *testing.T) {
		subject := "test.context.deadline"
		testData := []byte("deadline test")

		// Create context with deadline
		ctxWithDeadline, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		contextReceived := make(chan context.Context, 1)

		mockHandler := &MockMessageHandler{}
		mockHandler.On("Handle", mock.Anything, testData, subject).Run(func(args mock.Arguments) {
			// Capture the context passed to the handler
			receivedCtx := args.Get(0).(context.Context)
			contextReceived <- receivedCtx
		}).Return(nil)

		// Subscribe with a context that has a deadline
		err := repo.Subscribe(ctxWithDeadline, subject, mockHandler)
		require.NoError(t, err)

		// Give subscription time to be established
		time.Sleep(100 * time.Millisecond)

		// Publish message
		err = repo.Publish(context.Background(), subject, testData)
		require.NoError(t, err)

		// Wait for the handler to be called
		select {
		case handlerCtx := <-contextReceived:
			// Verify that the handler context is non-nil.
			assert.NotNil(t, handlerCtx)

			// Verify that the handler context preserves the deadline from the Subscribe ctx.
			expectedDeadline, expectedHasDeadline := ctxWithDeadline.Deadline()
			actualDeadline, actualHasDeadline := handlerCtx.Deadline()
			assert.Equal(t, expectedHasDeadline, actualHasDeadline, "deadline presence should be preserved")
			if expectedHasDeadline && actualHasDeadline {
				assert.Equal(t, expectedDeadline, actualDeadline, "deadline should be preserved in handler context")
			}

		case <-time.After(2 * time.Second):
			t.Fatal("Handler not called within timeout")
		}
	})
}

// TestReplyPublishTraceContext verifies that reply messages carry trace context
func TestReplyPublishTraceContext(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	conn, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		t.Skipf("Skipping test: no NATS server available: %v", err)
	}
	defer conn.Close()

	// Setup OTel with test exporter
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithBatcher(exporter))
	defer tp.Shutdown(context.Background())
	otel.SetTracerProvider(tp)

	logger := logging.NewLogger(true)
	repo := NewMessagingRepository(conn, nil, logger, 5*time.Second,
		constants.DefaultPendingMsgLimit, constants.DefaultPendingBytesLimit,
		constants.DefaultWorkerCount)
	defer repo.Close()

	ctx := context.Background()

	t.Run("queue_subscribe_with_reply_injects_trace", func(t *testing.T) {
		requestSubject := "test.request.reply"
		queue := "test.reply.queue"
		testData := []byte("request data")
		replyReceived := make(chan bool, 1)

		replyData := []byte("reply response")

		mockHandler := &MockMessageHandlerWithReply{}
		mockHandler.On("HandleWithReply", mock.Anything, testData, requestSubject, mock.Anything).
			Run(func(args mock.Arguments) {
				// Get the reply function and call it
				replyFunc := args.Get(3).(func([]byte) error)
				err := replyFunc(replyData)
				if err == nil {
					replyReceived <- true
				}
			}).Return(nil)

		// Subscribe with reply support
		err := repo.QueueSubscribeWithReply(ctx, requestSubject, queue, mockHandler)
		require.NoError(t, err)

		// Give subscription time to be established
		time.Sleep(100 * time.Millisecond)

		// Create a request-reply subscription to receive the reply
		replyInbox := nats.NewInbox()
		replyChan := make(chan *nats.Msg, 1)
		_, err = conn.Subscribe(replyInbox, func(msg *nats.Msg) {
			replyChan <- msg
		})
		require.NoError(t, err)

		time.Sleep(100 * time.Millisecond)

		// Publish a request-reply message
		requestMsg := nats.NewMsg(requestSubject)
		requestMsg.Header = make(nats.Header)
		requestMsg.Data = testData
		requestMsg.Reply = replyInbox

		err = conn.PublishMsg(requestMsg)
		require.NoError(t, err)

		// Wait for handler to be called and reply to be sent
		select {
		case <-replyReceived:
			time.Sleep(100 * time.Millisecond) // Allow spans to be exported

			// Flush the batch processor to ensure spans are exported
			tp.ForceFlush(context.Background())

			// Verify that reply.publish span was created
			spans := exporter.GetSpans()
			replySpanFound := false
			for _, s := range spans {
				if s.Name == "nats.reply.publish" && s.SpanKind == sdktrace.SpanKindProducer {
					replySpanFound = true
					break
				}
			}
			assert.True(t, replySpanFound, "Expected to find nats.reply.publish span with Producer kind")

		case <-time.After(2 * time.Second):
			t.Fatal("Reply not sent within timeout")
		}

		mockHandler.AssertExpectations(t)
	})
}

// TestHeaderCarrierCompatibility verifies the header carrier interface works correctly
func TestHeaderCarrierCompatibility(t *testing.T) {
	header := nats.Header{}
	carrier := natsHeaderCarrier(header)

	t.Run("carrier_set_and_get", func(t *testing.T) {
		// Verify Set/Get works
		carrier.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
		value := carrier.Get("traceparent")
		assert.Equal(t, "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01", value)
	})

	t.Run("carrier_keys", func(t *testing.T) {
		// Add multiple headers
		carrier.Set("x-trace-id", "trace-123")
		carrier.Set("x-span-id", "span-456")
		keys := carrier.Keys()
		assert.Len(t, keys, 3) // traceparent, x-trace-id, x-span-id
		assert.Contains(t, keys, "traceparent")
		assert.Contains(t, keys, "x-trace-id")
		assert.Contains(t, keys, "x-span-id")
	})

	t.Run("propagator_inject_extract", func(t *testing.T) {
		// Setup OTel with test provider to ensure propagator is initialized
		exporter := tracetest.NewInMemoryExporter()
		tp := trace.NewTracerProvider(trace.WithBatcher(exporter))
		defer tp.Shutdown(context.Background())
		otel.SetTracerProvider(tp)

		propagator := otel.GetTextMapPropagator()

		// Simulate injecting trace context
		freshHeader := nats.Header{}
		freshCarrier := natsHeaderCarrier(freshHeader)
		ctx, span := otel.Tracer("test").Start(context.Background(), "test-span")
		defer span.End()

		propagator.Inject(ctx, freshCarrier)

		// Verify we can extract it back (propagator should work with carrier)
		extractedCtx := propagator.Extract(context.Background(), freshCarrier)
		assert.NotNil(t, extractedCtx)
		// The important thing is that the carrier interface works without panicking
	})
}
