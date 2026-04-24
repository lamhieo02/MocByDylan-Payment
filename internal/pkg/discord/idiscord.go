package discord

// INotifier is the single entry-point for sending Discord notifications.
// Callers choose the destination by passing a ChannelName; the implementation
// resolves the matching webhook URL from its Config.
type INotifier interface {
	Send(channel ChannelName, color string, message string, fields ...EmbedFields) error
}

// compile-time guard
var _ INotifier = (*client)(nil)
