package payment

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/inbox"
)

const (
	// DefaultWebhookBodyLimit is deliberately smaller than the general API
	// request limit: provider webhooks contain facts, not customer documents.
	DefaultWebhookBodyLimit = 1 << 20
	maxWebhookBodyLimit     = 4 << 20
	webhookVerifyTimeout    = 10 * time.Second
)

var (
	ErrWebhookBodyTooLarge       = errors.New("payment webhook body is too large")
	ErrWebhookProviderMismatch   = errors.New("payment webhook provider mismatch")
	ErrWebhookPayloadHash        = errors.New("payment webhook payload hash mismatch")
	ErrWebhookPayloadUnavailable = errors.New("payment webhook payload could not be stored")
)

// PayloadStore durably stores the verified raw body in an encrypted or
// reference-backed store. PostgreSQL receives only the returned reference and
// hash; the body is never copied into audit, logs, or the inbox row.
type PayloadStore interface {
	Put(context.Context, PayloadPutInput) (string, error)
}

type PayloadPutInput struct {
	Source      string
	MessageID   string
	Payload     []byte
	PayloadHash [32]byte
	StoredAt    time.Time
}

// InboxStore is the small durable boundary needed by webhook receipt. The
// existing PostgreSQL platform store implements it.
type InboxStore interface {
	InsertInboxMessage(context.Context, inbox.Message) (bool, error)
}

type WebhookReceiver struct {
	adapter   Adapter
	inbox     InboxStore
	payloads  PayloadStore
	clock     clock.Clock
	bodyLimit int
}

type WebhookReceiveResult struct {
	Event     WebhookEvent
	Inserted  bool
	Duplicate bool
}

// NewWebhookReceiver constructs the verified-receipt boundary. Provider
// adapters are injected so this package remains independent of Stripe and
// other SDKs.
func NewWebhookReceiver(adapter Adapter, inboxStore InboxStore, payloads PayloadStore, timeSource clock.Clock, bodyLimit int) (*WebhookReceiver, error) {
	if adapter == nil || inboxStore == nil || payloads == nil {
		return nil, fmt.Errorf("payment webhook adapter, inbox, and payload store are required")
	}
	if bodyLimit == 0 {
		bodyLimit = DefaultWebhookBodyLimit
	}
	if bodyLimit < 1 || bodyLimit > maxWebhookBodyLimit {
		return nil, fmt.Errorf("payment webhook body limit is invalid")
	}
	if timeSource == nil {
		timeSource = clock.System{}
	}
	return &WebhookReceiver{adapter: adapter, inbox: inboxStore, payloads: payloads, clock: timeSource, bodyLimit: bodyLimit}, nil
}

// Receive authenticates the provider body before storing it, then inserts a
// deduplicated inbox message. A duplicate with the same source/message/hash is
// acknowledged without creating another durable message. Duplicate payload
// identity with a different hash is rejected for investigation.
func (receiver *WebhookReceiver) Receive(ctx context.Context, body []byte, headers map[string]string) (WebhookReceiveResult, error) {
	if receiver == nil || receiver.adapter == nil || receiver.inbox == nil || receiver.payloads == nil {
		return WebhookReceiveResult{}, fmt.Errorf("payment webhook receiver is not configured")
	}
	if len(body) == 0 || len(body) > receiver.bodyLimit {
		return WebhookReceiveResult{}, ErrWebhookBodyTooLarge
	}
	if ctx == nil {
		ctx = context.Background()
	}
	verifyCtx, cancel := context.WithTimeout(ctx, webhookVerifyTimeout)
	verified, err := receiver.adapter.VerifyWebhook(verifyCtx, append([]byte(nil), body...), cloneHeaders(headers))
	cancel()
	if err != nil {
		failure := Classify(err)
		return WebhookReceiveResult{}, failure
	}
	provider := strings.ToLower(strings.TrimSpace(receiver.adapter.Name()))
	if !validReference(provider, 1, 64) || strings.ToLower(strings.TrimSpace(verified.Provider)) != provider {
		return WebhookReceiveResult{}, ErrWebhookProviderMismatch
	}
	payloadHash := sha256.Sum256(body)
	if verified.PayloadSHA256 != payloadHash {
		return WebhookReceiveResult{}, ErrWebhookPayloadHash
	}
	if err := verified.Validate(); err != nil {
		return WebhookReceiveResult{}, err
	}
	storedAt := receiver.clock.Now().UTC()
	if storedAt.IsZero() {
		return WebhookReceiveResult{}, fmt.Errorf("payment webhook receipt time is invalid")
	}
	reference, err := receiver.payloads.Put(ctx, PayloadPutInput{Source: provider, MessageID: verified.EventID, Payload: append([]byte(nil), body...), PayloadHash: payloadHash, StoredAt: storedAt})
	if err != nil {
		return WebhookReceiveResult{}, Failure{Class: FailureTransient, Code: "payload_store_unavailable", SafeMessage: "The webhook payload could not be stored.", Cause: err}
	}
	if !validPayloadReference(reference) {
		return WebhookReceiveResult{}, ErrWebhookPayloadUnavailable
	}
	verified.Provider = provider
	verified.PayloadReference = reference
	if err := verified.Validate(); err != nil {
		return WebhookReceiveResult{}, err
	}
	messageID, err := identifier.New()
	if err != nil {
		return WebhookReceiveResult{}, fmt.Errorf("create payment webhook inbox ID: %w", err)
	}
	inserted, err := receiver.inbox.InsertInboxMessage(ctx, inbox.Message{ID: messageID, Source: provider, MessageID: verified.EventID, PayloadReference: reference, PayloadSHA256: payloadHash, AvailableAt: storedAt, ReceivedAt: storedAt, UpdatedAt: storedAt})
	if err != nil {
		if errors.Is(err, inbox.ErrPayloadConflict) {
			return WebhookReceiveResult{}, ErrWebhookPayloadHash
		}
		return WebhookReceiveResult{}, Failure{Class: FailureTransient, Code: "inbox_unavailable", SafeMessage: "The payment webhook could not be queued.", Cause: err}
	}
	return WebhookReceiveResult{Event: verified, Inserted: inserted, Duplicate: !inserted}, nil
}

func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

func validPayloadReference(value string) bool {
	return value != "" && len(value) <= 1000 && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0 && strings.IndexFunc(value, unicode.IsSpace) < 0
}
