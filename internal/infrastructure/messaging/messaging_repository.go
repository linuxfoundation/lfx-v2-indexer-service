// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package messaging provides NATS-based messaging infrastructure for the indexer service.
package messaging

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
)

// MessagingRepository implements the MessagingRepository interface for NATS operations
type MessagingRepository struct {
	conn              *nats.Conn
	authRepo          contracts.AuthRepository
	logger            *slog.Logger
	subscriptions     []*nats.Subscription
	consumeContexts   []jetstream.ConsumeContext
	mu                sync.RWMutex
	wg                sync.WaitGroup // tracks all in-flight handler goroutines (core NATS + JetStream)
	jsWg              sync.WaitGroup // tracks JetStream-only goroutines; waited before conn.Drain()
	drainTimeout      time.Duration
	isShuttingDown    bool
	pendingMsgLimit   int
	pendingBytesLimit int
	sem               chan struct{}
}

// NewMessagingRepository creates a new NATS messaging repository with auth delegation
func NewMessagingRepository(conn *nats.Conn, authRepo contracts.AuthRepository, logger *slog.Logger, drainTimeout time.Duration, pendingMsgLimit int, pendingBytesLimit int, workerCount int) *MessagingRepository {
	msgLogger := logging.WithComponent(logger, constants.ComponentNATS)

	if workerCount <= 0 {
		msgLogger.Warn("Invalid workerCount, falling back to default", "provided", workerCount, "default", constants.DefaultWorkerCount)
		workerCount = constants.DefaultWorkerCount
	}

	repo := &MessagingRepository{
		conn:              conn,
		authRepo:          authRepo,
		logger:            msgLogger,
		subscriptions:     make([]*nats.Subscription, 0),
		drainTimeout:      drainTimeout,
		isShuttingDown:    false,
		pendingMsgLimit:   pendingMsgLimit,
		pendingBytesLimit: pendingBytesLimit,
		sem:               make(chan struct{}, workerCount),
	}

	// Log initialization
	msgLogger.Info("NATS messaging repository initialized", "drain_timeout", drainTimeout, "worker_count", workerCount, "auth_repo_configured", authRepo != nil)

	return repo
}

// =================
// NATS MESSAGE OPERATIONS (preserved from MessageRepository)
// =================

// Subscribe subscribes to NATS messages
func (r *MessagingRepository) Subscribe(ctx context.Context, subject string, handler contracts.MessageHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.isShuttingDown {
		return fmt.Errorf("cannot subscribe to %s: repository is shutting down", subject)
	}

	if !r.IsConnected() {
		return fmt.Errorf("cannot subscribe to %s: NATS connection not available", subject)
	}

	r.logger.InfoContext(ctx, "Creating NATS subscription", "subject", subject)

	// Create the NATS message handler
	natsHandler := func(msg *nats.Msg) {
		// Extract trace context from incoming message headers, preserving cancellation/deadline from ctx.
		msgCtx := otel.GetTextMapPropagator().Extract(ctx, natsHeaderCarrier(msg.Header))
		data := append([]byte(nil), msg.Data...)
		msgSubject := msg.Subject
		r.wg.Add(1)
		r.sem <- struct{}{}
		go func() {
			defer func() {
				<-r.sem
				r.wg.Done()
			}()

			spanCtx, span := tracer.Start(msgCtx, "nats.process",
				trace.WithSpanKind(trace.SpanKindConsumer),
				trace.WithAttributes(
					attribute.String("messaging.system", "nats"),
					attribute.String("messaging.destination.name", msgSubject),
					attribute.String("messaging.operation.type", "process"),
					attribute.Int("messaging.message.body.size", len(data)),
				),
			)
			defer span.End()

			spanCtx, logger := logging.WithRequestID(spanCtx, r.logger)
			logger.Debug("NATS message received", "subject", msgSubject, "size", len(data))

			if err := handler.Handle(spanCtx, data, msgSubject); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				logger.Error("Message handler failed", "error", err.Error())
			} else {
				span.SetStatus(codes.Ok, "")
			}
		}()
	}

	// Subscribe to the subject
	sub, err := r.conn.Subscribe(subject, natsHandler)
	if err != nil {
		r.logger.Error("Failed to subscribe to NATS", "subject", subject, "error", err.Error())
		return fmt.Errorf("failed to subscribe to subject %s: %w", subject, err)
	}

	if err := sub.SetPendingLimits(r.pendingMsgLimit, r.pendingBytesLimit); err != nil {
		_ = sub.Unsubscribe()
		return fmt.Errorf("failed to set pending limits on subscription %s: %w", subject, err)
	}

	// Store the subscription for cleanup
	r.subscriptions = append(r.subscriptions, sub)

	r.logger.Info("NATS subscription created successfully", "subject", subject)
	return nil
}

// QueueSubscribe subscribes to NATS messages with queue group for load balancing
func (r *MessagingRepository) QueueSubscribe(ctx context.Context, subject string, queue string, handler contracts.MessageHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.isShuttingDown {
		return fmt.Errorf("cannot queue subscribe to %s: repository is shutting down", subject)
	}

	if !r.IsConnected() {
		return fmt.Errorf("cannot queue subscribe to %s: NATS connection not available", subject)
	}

	r.logger.InfoContext(ctx, "Creating NATS queue subscription", "subject", subject, "queue", queue)

	// Create the NATS message handler
	natsHandler := func(msg *nats.Msg) {
		// Extract trace context from incoming message headers, preserving cancellation/deadline from ctx.
		msgCtx := otel.GetTextMapPropagator().Extract(ctx, natsHeaderCarrier(msg.Header))
		// Deep-copy message data before handing off to goroutine
		data := append([]byte(nil), msg.Data...)
		msgSubject := msg.Subject
		r.wg.Add(1)
		r.sem <- struct{}{}
		go func() {
			defer func() {
				<-r.sem
				r.wg.Done()
			}()

			spanCtx, span := tracer.Start(msgCtx, "nats.process",
				trace.WithSpanKind(trace.SpanKindConsumer),
				trace.WithAttributes(
					attribute.String("messaging.system", "nats"),
					attribute.String("messaging.destination.name", msgSubject),
					attribute.String("messaging.operation.type", "process"),
					attribute.Int("messaging.message.body.size", len(data)),
				),
			)
			defer span.End()

			spanCtx, logger := logging.WithRequestID(spanCtx, r.logger)
			logger.Debug("NATS queue message received", "subject", msgSubject, "queue", queue, "size", len(data))

			if err := handler.Handle(spanCtx, data, msgSubject); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				logger.Error("Queue message handler failed", "queue", queue, "error", err.Error())
			} else {
				span.SetStatus(codes.Ok, "")
			}
		}()
	}

	// Subscribe to the subject with queue group
	sub, err := r.conn.QueueSubscribe(subject, queue, natsHandler)
	if err != nil {
		r.logger.Error("Failed to queue subscribe to NATS", "subject", subject, "queue", queue, "error", err.Error())
		return fmt.Errorf("failed to queue subscribe to subject %s with queue %s: %w", subject, queue, err)
	}

	if err := sub.SetPendingLimits(r.pendingMsgLimit, r.pendingBytesLimit); err != nil {
		_ = sub.Unsubscribe()
		return fmt.Errorf("failed to set pending limits on subscription %s: %w", subject, err)
	}

	// Store the subscription for cleanup
	r.subscriptions = append(r.subscriptions, sub)

	r.logger.Info("NATS queue subscription created successfully", "subject", subject, "queue", queue)
	return nil
}

// QueueSubscribeWithReply subscribes to NATS messages with queue group and reply support
func (r *MessagingRepository) QueueSubscribeWithReply(ctx context.Context, subject string, queue string, handler contracts.MessageHandlerWithReply) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.isShuttingDown {
		return fmt.Errorf("cannot queue subscribe with reply to %s: repository is shutting down", subject)
	}

	if !r.IsConnected() {
		return fmt.Errorf("cannot queue subscribe with reply to %s: NATS connection not available", subject)
	}

	r.logger.InfoContext(ctx, "Creating NATS queue subscription with reply", "subject", subject, "queue", queue)

	// Create the NATS message handler with reply support
	natsHandler := func(msg *nats.Msg) {
		// Extract trace context from incoming message headers, preserving cancellation/deadline from ctx.
		msgCtx := otel.GetTextMapPropagator().Extract(ctx, natsHeaderCarrier(msg.Header))
		// Deep-copy fields before msg may be reused after callback returns
		data := append([]byte(nil), msg.Data...)
		msgSubject := msg.Subject
		replySubject := msg.Reply

		r.wg.Add(1)
		r.sem <- struct{}{}
		go func() {
			defer func() {
				<-r.sem
				r.wg.Done()
			}()

			spanCtx, span := tracer.Start(msgCtx, "nats.process",
				trace.WithSpanKind(trace.SpanKindConsumer),
				trace.WithAttributes(
					attribute.String("messaging.system", "nats"),
					attribute.String("messaging.destination.name", msgSubject),
					attribute.String("messaging.operation.type", "process"),
					attribute.Int("messaging.message.body.size", len(data)),
				),
			)
			defer span.End()

			spanCtx, logger := logging.WithRequestID(spanCtx, r.logger)
			logger.Debug("NATS queue message with reply received", "subject", msgSubject, "queue", queue, "has_reply", replySubject != "")

			var replyFunc func([]byte) error
			if replySubject != "" {
				replyFunc = func(replyData []byte) error {
					logger.Debug("Sending reply to NATS message", "reply_subject", replySubject, "reply_size", len(replyData))
					// Create a Producer span for the reply and inject trace context into headers
					replyCtx, replySpan := tracer.Start(spanCtx, "nats.reply.publish",
						trace.WithSpanKind(trace.SpanKindProducer),
						trace.WithAttributes(
							attribute.String("messaging.system", "nats"),
							attribute.String("messaging.destination.name", replySubject),
							attribute.Int("messaging.message.body.size", len(replyData)),
						),
					)
					defer replySpan.End()

					replyMsg := nats.NewMsg(replySubject)
					replyMsg.Header = make(nats.Header)
					replyMsg.Data = replyData
					otel.GetTextMapPropagator().Inject(replyCtx, natsHeaderCarrier(replyMsg.Header))

					if err := r.conn.PublishMsg(replyMsg); err != nil {
						replySpan.RecordError(err)
						replySpan.SetStatus(codes.Error, err.Error())
						logger.Error("Failed to send reply", "reply_subject", replySubject, "error", err.Error())
						return err
					}
					replySpan.SetStatus(codes.Ok, "")
					return nil
				}
			}

			if err := handler.HandleWithReply(spanCtx, data, msgSubject, replyFunc); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				logger.Error("Queue message with reply handler failed", "queue", queue, "error", err.Error())
			} else {
				span.SetStatus(codes.Ok, "")
			}
		}()
	}

	// Subscribe to the subject with queue group
	sub, err := r.conn.QueueSubscribe(subject, queue, natsHandler)
	if err != nil {
		r.logger.Error("Failed to queue subscribe with reply to NATS", "subject", subject, "queue", queue, "error", err.Error())
		return fmt.Errorf("failed to queue subscribe with reply to subject %s with queue %s: %w", subject, queue, err)
	}

	if err := sub.SetPendingLimits(r.pendingMsgLimit, r.pendingBytesLimit); err != nil {
		_ = sub.Unsubscribe()
		return fmt.Errorf("failed to set pending limits on subscription %s: %w", subject, err)
	}

	// Store the subscription for cleanup
	r.subscriptions = append(r.subscriptions, sub)

	r.logger.Info("NATS queue subscription with reply created successfully", "subject", subject, "queue", queue)
	return nil
}

// Publish publishes a message to NATS
func (r *MessagingRepository) Publish(ctx context.Context, subject string, data []byte) error {
	logger := logging.FromContext(ctx, r.logger)

	logger.Debug("Publishing message to NATS", "subject", subject, "size", len(data))

	// Check connection state before publishing
	if !r.IsConnected() {
		r.logger.Error("Cannot publish: NATS connection not available", "subject", subject)
		return fmt.Errorf("NATS connection not available for publishing to subject %s", subject)
	}

	ctx, span := tracer.Start(ctx, "nats.publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination.name", subject),
			attribute.Int("messaging.message.body.size", len(data)),
		),
	)
	defer span.End()

	msg := nats.NewMsg(subject)
	msg.Header = make(nats.Header)
	msg.Data = data
	otel.GetTextMapPropagator().Inject(ctx, natsHeaderCarrier(msg.Header))

	if err := r.conn.PublishMsg(msg); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		logger.Error("Failed to publish message to NATS", "subject", subject, "error", err.Error())
		return fmt.Errorf("failed to publish message to subject %s: %w", subject, err)
	}

	span.SetStatus(codes.Ok, "")
	logger.Debug("Message published to NATS successfully", "subject", subject, "size", len(data))
	return nil
}

// waitForHandlers waits for in-flight message handler goroutines to finish,
// bounded by the provided timeout so shutdown cannot hang indefinitely.
// Returns true if the wait timed out, false if all handlers finished in time.
func (r *MessagingRepository) waitForHandlers(timeout time.Duration) bool {
	if timeout <= 0 {
		r.logger.Warn("No time remaining to wait for in-flight NATS handlers")
		return false
	}
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return false
	case <-time.After(timeout):
		r.logger.Warn("Timed out waiting for in-flight NATS handlers", "timeout", timeout)
		return true
	}
}

// waitForJetStreamWorkers waits for all goroutines tracked by r.jsWg to finish,
// bounded by timeout. Returns true if it timed out.
func (r *MessagingRepository) waitForJetStreamWorkers(timeout time.Duration) bool {
	if timeout <= 0 {
		r.logger.Warn("No time remaining to wait for in-flight JetStream workers")
		return true
	}
	done := make(chan struct{})
	go func() {
		r.jsWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return false
	case <-time.After(timeout):
		r.logger.Warn("Timed out waiting for in-flight JetStream workers", "timeout", timeout)
		return true
	}
}

// DrainWithTimeout performs graceful NATS connection drain with timeout
func (r *MessagingRepository) DrainWithTimeout() error {
	r.mu.Lock()
	r.isShuttingDown = true
	totalSubscriptions := len(r.subscriptions)
	consumeContexts := r.consumeContexts
	r.consumeContexts = nil
	r.mu.Unlock()

	// Record the deadline before stopping consumers so the entire
	// shutdown sequence — consumer stop plus connection drain — is
	// bounded by drainTimeout.
	deadline := time.Now().Add(r.drainTimeout)

	// Phase 1: Stop JetStream consumers so no new callbacks fire.
	// Stop() returns once the currently-executing callback (if any) has
	// returned, so after this loop the only running JetStream work is inside
	// goroutines that were already spawned by a callback.
	for _, cc := range consumeContexts {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			r.logger.Warn("Drain budget exhausted before all JetStream consumers stopped")
			break
		}
		stopped := make(chan struct{}, 1)
		go func(c jetstream.ConsumeContext) { c.Stop(); stopped <- struct{}{} }(cc)
		select {
		case <-stopped:
		case <-time.After(remaining):
			r.logger.Warn("Timed out waiting for JetStream consumer to stop",
				"remaining", remaining)
		}
	}

	// Phase 2: Wait for all in-flight JetStream goroutines to finish BEFORE
	// draining the NATS connection. These goroutines publish domain events via
	// conn; if conn.Drain() closes the connection first, those publishes fail.
	// jsWg is separate from wg so we don't accidentally wait for core-NATS
	// callbacks that are still active on the open connection.
	r.waitForJetStreamWorkers(time.Until(deadline))

	r.logger.Info("Starting NATS graceful drain sequence", "timeout", r.drainTimeout, "subscriptions", totalSubscriptions)

	if r.conn == nil {
		r.logger.Warn("NATS connection is nil, skipping drain")
		r.waitForHandlers(time.Until(deadline))
		return nil
	}

	if r.conn.IsClosed() {
		r.logger.Info("NATS connection already closed, no drain needed")
		r.waitForHandlers(time.Until(deadline))
		return nil
	}

	if r.conn.IsDraining() {
		r.logger.Info("NATS connection already draining, waiting for completion")
		// Callbacks can still fire until the connection is fully closed, so
		// wait for IsClosed before calling wg.Wait() to avoid a concurrent
		// Add/Wait panic.
		drainDone := make(chan struct{})
		go func() {
			for !r.conn.IsClosed() {
				time.Sleep(100 * time.Millisecond)
			}
			close(drainDone)
		}()
		select {
		case <-drainDone:
		case <-time.After(time.Until(deadline)):
			r.logger.Warn("Timed out waiting for ongoing NATS drain", "timeout", r.drainTimeout)
			return fmt.Errorf("timed out waiting for ongoing NATS drain after %s", r.drainTimeout)
		}
		r.waitForHandlers(time.Until(deadline))
		return nil
	}

	// Phase 3: Drain core NATS subscriptions.
	r.logger.Debug("Initiating NATS connection drain")

	if err := r.conn.Drain(); err != nil {
		r.logger.Error("Failed to start NATS drain", "error", err.Error())
		return fmt.Errorf("failed to drain NATS connection: %w", err)
	}

	// Wait for drain to complete, bounded by the remaining deadline.
	drainTimedOut := false
	select {
	case <-time.After(time.Until(deadline)):
		drainTimedOut = true
		r.logger.Warn("NATS drain timeout reached", "timeout", r.drainTimeout)
	case <-func() <-chan struct{} {
		done := make(chan struct{})
		go func() {
			for !r.conn.IsClosed() {
				time.Sleep(100 * time.Millisecond)
			}
			close(done)
		}()
		return done
	}():
		// Drain completed before timeout
	}

	// Phase 4: Wait for core-NATS handler goroutines, bounded by remaining budget.
	// conn.Drain() only waits for subscriber callbacks to return, not for
	// goroutines they spawn; r.wg covers those spawned goroutines.
	handlersTimedOut := r.waitForHandlers(time.Until(deadline))

	if drainTimedOut || handlersTimedOut {
		r.logger.Warn("NATS drain did not complete cleanly", "drain_timed_out", drainTimedOut, "handlers_timed_out", handlersTimedOut, "subscriptions_processed", totalSubscriptions)
		switch {
		case drainTimedOut && handlersTimedOut:
			return fmt.Errorf("NATS drain and in-flight handler wait both timed out after %s", r.drainTimeout)
		case drainTimedOut:
			return fmt.Errorf("NATS drain timed out after %s", r.drainTimeout)
		default: // handlersTimedOut only
			return fmt.Errorf("NATS drain completed but timed out waiting for in-flight handlers to finish")
		}
	}

	r.logger.Info("NATS drain completed successfully", "subscriptions_processed", totalSubscriptions)
	return nil
}

// Close closes the NATS connection and all subscriptions
func (r *MessagingRepository) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	logger := r.logger
	totalSubscriptions := len(r.subscriptions)

	logger.Info("Initiating NATS repository close sequence", "total_subscriptions", totalSubscriptions)

	// First, attempt graceful drain if not already shutting down
	if !r.isShuttingDown {
		logger.Info("Close called without drain, attempting graceful drain first")
		r.mu.Unlock() // Unlock to call DrainWithTimeout
		if err := r.DrainWithTimeout(); err != nil {
			logger.Warn("Graceful drain failed, proceeding with immediate close", "error", err.Error())
		}
		r.mu.Lock() // Re-lock for the rest of the method
	}

	// Unsubscribe from all subscriptions with detailed tracking
	subscriptionErrors := 0
	for i, sub := range r.subscriptions {
		if sub == nil {
			logger.Warn("Null subscription found during cleanup", "index", i)
			continue
		}

		if !sub.IsValid() {
			logger.Debug("Invalid subscription found during cleanup", "index", i)
			continue
		}

		logger.Debug("Unsubscribing from subscription",
			"index", i,
			"subject", sub.Subject,
			"queue", sub.Queue)

		if err := sub.Unsubscribe(); err != nil {
			subscriptionErrors++
			logger.Error("Failed to unsubscribe from subscription", "index", i, "subject", sub.Subject, "error", err.Error())
		} else {
			logger.Debug("Successfully unsubscribed from subscription",
				"index", i,
				"subject", sub.Subject,
				"queue", sub.Queue)
		}
	}

	// Clear subscriptions array
	r.subscriptions = nil

	logger.Info("Subscription cleanup completed",
		"total_subscriptions", totalSubscriptions,
		"subscription_errors", subscriptionErrors,
		"success_rate", fmt.Sprintf("%.2f%%", float64(totalSubscriptions-subscriptionErrors)/float64(totalSubscriptions)*100))

	// Close the connection if not already closed
	if r.conn != nil && !r.conn.IsClosed() {
		logger.Info("Closing NATS connection",
			"connection_status", r.conn.Status().String())

		r.conn.Close()

		logger.Info("NATS connection closed successfully")
	} else {
		logger.Info("NATS connection already closed or nil")
	}

	logger.Info("NATS repository close sequence completed", "subscription_errors", subscriptionErrors)
	return nil
}

// HealthCheck checks the health of the NATS connection
func (r *MessagingRepository) HealthCheck(ctx context.Context) error {
	r.logger.Debug("NATS health check initiated")

	// Check basic connection state
	if r.conn == nil {
		r.logger.Warn("NATS health check failed: connection is nil")
		return fmt.Errorf("%s: NATS connection is nil", constants.ErrHealthCheck)
	}

	r.logger.Debug("NATS connection diagnostics", "status", r.conn.Status().String(), "connected", r.conn.IsConnected())

	if !r.conn.IsConnected() {
		r.logger.Warn("NATS health check failed: connection not connected", "status", r.conn.Status().String())
		return fmt.Errorf("%s: NATS connection is not connected", constants.ErrHealthCheck)
	}

	// Check if we can get server info
	if r.conn.Status() != nats.CONNECTED {
		r.logger.Warn("NATS health check failed: connection status not connected",
			"status", r.conn.Status().String(),
			"expected", nats.CONNECTED.String())
		return fmt.Errorf("%s: NATS connection status is not connected: %s", constants.ErrHealthCheck, r.conn.Status())
	}

	// Check subscription health
	r.mu.RLock()
	totalSubs := len(r.subscriptions)
	activeSubs := 0
	for _, sub := range r.subscriptions {
		if sub != nil && sub.IsValid() {
			activeSubs++
		}
	}
	r.mu.RUnlock()

	healthRatio := float64(activeSubs) / float64(totalSubs) * 100
	if totalSubs > 0 && healthRatio < 90 {
		r.logger.Warn("NATS subscription health degraded",
			"total_subscriptions", totalSubs,
			"active_subscriptions", activeSubs,
			"health_ratio", fmt.Sprintf("%.2f%%", healthRatio))
	}

	// Check auth repository if configured
	if r.authRepo != nil {
		if err := r.authRepo.HealthCheck(ctx); err != nil {
			r.logger.Warn("NATS auth repository health check failed",
				"error", err.Error())
			// Don't fail the overall health check for auth issues
		}
	}

	r.logger.Info("NATS health check completed successfully", "status", "healthy", "subscriptions", totalSubs)

	return nil
}

// =================
// AUTHENTICATION OPERATIONS (delegated to AuthRepository)
// =================

// ValidateToken validates a JWT token and returns principal information
func (r *MessagingRepository) ValidateToken(ctx context.Context, token string) (*contracts.Principal, error) {
	r.logger.Debug("NATS delegating token validation to auth repository")

	if r.authRepo == nil {
		r.logger.Error("Token validation failed: auth repository not configured")
		return nil, fmt.Errorf(constants.ErrAuthRepoNotConfigured)
	}

	principal, err := r.authRepo.ValidateToken(ctx, token)
	if err != nil {
		r.logger.Error("Token validation delegation failed", "error", err.Error())
		return nil, err
	}

	r.logger.Debug("Token validation delegation completed successfully", "principal", principal.Principal)
	return principal, nil
}

// ParsePrincipals parses principals from HTTP headers with delegation support
func (r *MessagingRepository) ParsePrincipals(ctx context.Context, headers map[string]string) ([]contracts.Principal, error) {
	r.logger.Debug("NATS delegating principal parsing to auth repository", "headers_count", len(headers))

	if r.authRepo == nil {
		r.logger.Error("Principal parsing failed: auth repository not configured")
		return nil, fmt.Errorf(constants.ErrAuthRepoNotConfigured)
	}

	principals, err := r.authRepo.ParsePrincipals(ctx, headers)
	if err != nil {
		r.logger.Error("Principal parsing delegation failed", "error", err.Error())
		return nil, err
	}

	r.logger.Debug("Principal parsing delegation completed successfully", "principals_count", len(principals))
	return principals, nil
}

// =================
// UTILITY METHODS (preserved)
// =================

// GetConnection returns the underlying NATS connection
func (r *MessagingRepository) GetConnection() *nats.Conn {
	return r.conn
}

// GetSubscriptionCount returns the number of active subscriptions
func (r *MessagingRepository) GetSubscriptionCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.subscriptions)
}

// IsConnected checks if the NATS connection is active
func (r *MessagingRepository) IsConnected() bool {
	return r.conn != nil && r.conn.IsConnected()
}

// GetMetrics returns messaging repository metrics for monitoring
func (r *MessagingRepository) GetMetrics() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Calculate subscription health
	totalSubs := len(r.subscriptions)
	activeSubs := 0
	for _, sub := range r.subscriptions {
		if sub != nil && sub.IsValid() {
			activeSubs++
		}
	}

	healthRatio := float64(0)
	if totalSubs > 0 {
		healthRatio = float64(activeSubs) / float64(totalSubs) * 100
	}

	connectionStatus := "unknown"
	connected := false
	if r.conn != nil {
		connectionStatus = r.conn.Status().String()
		connected = r.conn.IsConnected()
	}

	workerCapacity := cap(r.sem)
	workersInFlight := len(r.sem)

	return map[string]interface{}{
		"connection_status":    connectionStatus,
		"connected":            connected,
		"total_subscriptions":  totalSubs,
		"active_subscriptions": activeSubs,
		"subscription_health":  fmt.Sprintf("%.2f%%", healthRatio),
		"is_shutting_down":     r.isShuttingDown,
		"drain_timeout":        r.drainTimeout,
		"auth_repo_configured": r.authRepo != nil,
		"worker_capacity":      workerCapacity,
		"workers_in_flight":    workersInFlight,
	}
}

// GetConnectionStatus returns detailed connection status for monitoring
func (r *MessagingRepository) GetConnectionStatus() map[string]interface{} {
	status := map[string]interface{}{
		"component":          "messaging_repository",
		"status":             "initialized",
		"subscription_count": len(r.subscriptions),
		"is_shutting_down":   r.isShuttingDown,
	}

	if r.conn != nil && r.conn.IsConnected() {
		status["status"] = "connected"
		if r.conn.ConnectedUrl() != "" {
			status["connected_url"] = r.conn.ConnectedUrl()
		}
	} else if r.conn != nil {
		switch {
		case r.conn.IsClosed():
			status["status"] = "closed"
		case r.conn.IsReconnecting():
			status["status"] = "reconnecting"
		}
	}

	return status
}

// =================
// JETSTREAM OPERATIONS
// =================

// ConsumeWithJetStream creates a durable JetStream consumer on streamName,
// filtering on filterSubjects, and delivers messages to handler. On handler
// error the message is NAKed with exponential-backoff jitter; on success it
// is ACKed. The consumer is stopped automatically when DrainWithTimeout is
// called.
func (r *MessagingRepository) ConsumeWithJetStream(
	ctx context.Context,
	streamName string,
	filterSubjects []string,
	handler func(context.Context, []byte, string) error,
) error {
	js, err := jetstream.New(r.conn)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to create JetStream client for consumer",
			"error", err,
			"stream", streamName,
			"consumer", constants.ConsumerNameIndexer)
		return fmt.Errorf("failed to create JetStream client: %w", err)
	}

	cfg := jetstream.ConsumerConfig{
		Name:           constants.ConsumerNameIndexer,
		Durable:        constants.ConsumerNameIndexer,
		FilterSubjects: filterSubjects,
		AckPolicy:      jetstream.AckExplicitPolicy,
		MaxDeliver:     5,
		AckWait:        30 * time.Second,
		MaxAckPending:  100,
		// DeliverNewPolicy ensures a freshly-created durable consumer only
		// processes messages published after it was created. On pod restart the
		// NATS server restores the consumer's ACK floor, so in-progress messages
		// are redelivered and no messages are skipped. Without this the initial
		// consumer creation would replay the full 24 h stream history.
		DeliverPolicy: jetstream.DeliverNewPolicy,
	}

	consumer, err := js.CreateOrUpdateConsumer(ctx, streamName, cfg)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to create JetStream durable consumer",
			"error", err,
			"stream", streamName,
			"consumer", cfg.Name)
		return fmt.Errorf("failed to create JetStream consumer on stream %s: %w", streamName, err)
	}

	consumeCtx, err := consumer.Consume(func(msg jetstream.Msg) {
		// Extract trace context and snapshot message fields while still
		// in the consumer callback goroutine (msg is only valid here).
		msgCtx := otel.GetTextMapPropagator().Extract(ctx, natsHeaderCarrier(msg.Headers()))
		data := append([]byte(nil), msg.Data()...)
		subject := msg.Subject()

		// Acquire a worker slot; this blocks the consumer callback goroutine
		// until a slot is free, which provides back-pressure alongside
		// MaxAckPending and keeps OpenSearch write concurrency bounded.
		// Track in r.wg (all handlers) AND r.jsWg (JetStream-only) so
		// DrainWithTimeout can wait for JetStream workers before draining the
		// connection (preventing domain-event publishes on a closed connection).
		r.wg.Add(1)
		r.jsWg.Add(1)
		r.sem <- struct{}{}
		go func() {
			defer func() {
				<-r.sem
				r.jsWg.Done()
				r.wg.Done()
			}()

			spanCtx, span := tracer.Start(msgCtx, "jetstream.process",
				trace.WithSpanKind(trace.SpanKindConsumer),
				trace.WithAttributes(
					attribute.String("messaging.system", "nats"),
					attribute.String("messaging.destination.name", subject),
					attribute.String("messaging.operation.type", "process"),
					attribute.Int("messaging.message.body.size", len(data)),
				),
			)
			defer span.End()

			if err := handler(spanCtx, data, subject); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				r.logger.ErrorContext(spanCtx, "JetStream message handler failed — NAKing with backoff",
					"error", err,
					"subject", subject,
					"consumer", cfg.Name)
				if nakErr := msg.NakWithDelay(nakDelay(msg)); nakErr != nil {
					r.logger.ErrorContext(spanCtx, "Failed to NAK JetStream message",
						"error", nakErr,
						"subject", subject)
				}
				return
			}
			span.SetStatus(codes.Ok, "")
			if ackErr := msg.Ack(); ackErr != nil {
				r.logger.ErrorContext(spanCtx, "Failed to ACK JetStream message",
					"error", ackErr,
					"subject", subject)
			}
		}()
	})
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to start JetStream consume loop",
			"error", err,
			"stream", streamName,
			"consumer", cfg.Name)
		return fmt.Errorf("failed to start JetStream consume loop: %w", err)
	}

	r.logger.InfoContext(ctx, "JetStream durable consumer started",
		"stream", streamName,
		"consumer", cfg.Name,
		"filter_subjects", filterSubjects)

	// Guard against a race where DrainWithTimeout has already copied and
	// cleared consumeContexts before we append: if shutdown has started,
	// stop the consumer immediately so it is not leaked.
	r.mu.Lock()
	if r.isShuttingDown {
		r.mu.Unlock()
		consumeCtx.Stop()
		return fmt.Errorf("cannot start JetStream consumer on %s: messaging repository is shutting down", streamName)
	}
	r.consumeContexts = append(r.consumeContexts, consumeCtx)
	r.mu.Unlock()

	return nil
}

// nakDelay returns an exponential backoff duration with full jitter based on
// the message delivery attempt count. Full jitter (random in [0, cap])
// prevents correlated retries across concurrent service replicas.
//
// Attempt 1 → rand(0, 1s)
// Attempt 2 → rand(0, 2s)
func nakDelay(msg jetstream.Msg) time.Duration {
	meta, err := msg.Metadata()
	if err != nil || meta == nil {
		return time.Second
	}
	maxDelay := time.Second * time.Duration(math.Pow(2, float64(meta.NumDelivered-1)))
	return time.Duration(rand.Int63n(int64(maxDelay) + 1))
}
