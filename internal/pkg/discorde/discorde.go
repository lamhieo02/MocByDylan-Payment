package discorde

import (
	"context"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/pkg/discord"
)

// Init wires the global hub with a configured notifier and options.
func Init(notifyAPI discord.INotifier, options *ClientOptions) error {
	hub := CurrentHub()
	client, err := NewClient(notifyAPI, options)
	if err != nil {
		return err
	}
	hub.BindClient(client)
	return nil
}

func CaptureExeption(exception error) {
	hub := CurrentHub()
	hub.CaptureException(exception)
}

func CaptureMessage(message string) {
	hub := CurrentHub()
	hub.CaptureMessage(message)
}

func WithScope(f func(scope *Scope)) {
	hub := CurrentHub()
	hub.WithScope(f)
}

// SendErrToDiscord sends an error event to the default error channel.
func SendErrToDiscord(_ context.Context, input, method, requestID string, err error) {
	go WithScope(func(scope *Scope) {
		defer func() { recover() }() //nolint:errcheck
		scope.SetTag("input", input)
		scope.SetTag("method", method)
		scope.SetTag("request_id", requestID)
		CaptureExeption(err)
	})
}

// SendWarningToDiscord sends an error event to the warning channel.
func SendWarningToDiscord(_ context.Context, input, method, requestID string, err error) {
	go WithScope(func(scope *Scope) {
		defer func() { recover() }() //nolint:errcheck
		scope.SetTag("input", input)
		scope.SetTag("method", method)
		scope.SetTag("request_id", requestID)
		scope.SetChannel(discord.ChannelWarning)
		CaptureExeption(err)
	})
}

// SendNoticeNotFoundConfigAddressToDiscord sends an event to the notice-config-address channel.
func SendNoticeNotFoundConfigAddressToDiscord(_ context.Context, input, method, requestID string, err error) {
	go WithScope(func(scope *Scope) {
		defer func() { recover() }() //nolint:errcheck
		scope.SetTag("input", input)
		scope.SetTag("method", method)
		scope.SetTag("request_id", requestID)
		scope.SetChannel(discord.ChannelNoticeConfigAddress)
		CaptureExeption(err)
	})
}

// SendNotifyOrderToDiscord sends an order notification to the MocByDylan Notify Order channel.
func SendNotifyOrderToDiscord(_ context.Context, input, method, requestID string, err error) {
	go WithScope(func(scope *Scope) {
		defer func() { recover() }() //nolint:errcheck
		scope.SetTag("input", input)
		scope.SetTag("method", method)
		scope.SetTag("request_id", requestID)
		scope.SetChannel(discord.ChannelNotifyOrder)
		CaptureExeption(err)
	})
}
