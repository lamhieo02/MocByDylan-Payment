package discord

import (
	"fmt"
	"net/http"
	"time"
)

type client struct {
	cfg  Config
	http *http.Client
}

// New creates a new Discord notifier from the provided Config.
func New(cfg Config) INotifier {
	return &client{
		cfg: cfg,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Send routes the message to the webhook URL registered under channel.
// Falls back to ChannelDefault when the requested channel has no URL.
func (c *client) Send(channel ChannelName, color string, message string, fields ...EmbedFields) error {
	webhookURL, ok := c.cfg.Channels[channel]
	if !ok || webhookURL == "" {
		webhookURL = c.cfg.Channels[ChannelDefault]
	}
	if webhookURL == "" {
		return fmt.Errorf("discord: no webhook URL configured for channel %q", channel)
	}

	iconURL := c.cfg.IconURL
	if iconURL == "" {
		iconURL = logoURL
	}

	wh := Webhook{
		Username:  c.cfg.Username,
		AvatarUrl: c.cfg.AvatarURL,
	}

	emb := Embed{
		Title:       message,
		Description: message,
		Timestamp:   time.Now().UTC().Format("2006-01-02T15:04:05-0700"),
		Color:       getColor(color),
		Thumbnail: EmbedThumbnail{
			Url: iconURL,
		},
		Footer: EmbedFooter{
			Text:    "Sent via MocByDylan",
			IconUrl: iconURL,
		},
		Author: EmbedAuthor{
			Name:    "MocByDylan",
			IconUrl: iconURL,
		},
	}
	emb.Fields = append(emb.Fields, fields...)
	wh.Embeds = append(wh.Embeds, emb)

	return sendMessage(c.http, webhookURL, wh, true)
}
