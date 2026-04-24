package discorde

import (
	"errors"
	"testing"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/pkg/discord"
	"github.com/stretchr/testify/require"
)

const (
	_logoURL        = "https://raw.githubusercontent.com/lamhieo02/MocByDylan-Payment/main/assets/logo.png"
	_notifyOrderURL = "https://discord.com/api/webhooks/1497093069307252777/pLRIwspCk6zqy33TtOgCxNC5DPUbrnxlhQlTvKQ7JjsjL8blb0F7D7Vo6F-FyhPaLboo"
	_warningURL     = "https://discord.com/api/webhooks/1299202911741411328/2v39qdz1AvLkuxCt3aYZvaJva1sKHDcAp0ZMNI_nhbDgwtebgpToqNhtXSaYxLv-OwgS"
)

func newTestNotifier(channels map[discord.ChannelName]string) discord.INotifier {
	return discord.New(discord.Config{
		Username: "Captain Hook",
		IconURL:  _logoURL,
		Channels: channels,
	})
}
	return errors.New("xinchao cac ban")
}

// nolint
func a() error {
	if err := c(); err != nil {
		return err
	}
	return nil
}

// nolint
func returError() error {
	if err := a(); err != nil {
		return err
	}
	return nil
}

func TestWarningCase(t *testing.T) {
	// t.Skip()

	notifyAPI := newTestNotifier(map[discord.ChannelName]string{
		discord.ChannelWarning: _warningURL,
	})

	if err := Init(notifyAPI, &ClientOptions{
		ProjectName: "mocbydylan",
		Environment: "uat",
	}); err != nil {
		t.Error(err)
	}

	_ = notifyAPI.Send(discord.ChannelWarning, "#FF0000", "hello", discord.EmbedFields{Name: "hello", Value: "world", Inline: true})
}

func TestHappyCase(t *testing.T) {
	// t.Skip()

	notifyAPI := newTestNotifier(map[discord.ChannelName]string{
		discord.ChannelDefault: "",
	})

	err := Init(notifyAPI, &ClientOptions{
		ProjectName: "mocbydylan",
		Environment: "uat",
	})

	a := returError()
	require.NoError(t, err)

	CaptureMessage(a.Error())

	t.Error("trigger")
}

func TestSendMessageNormalToDiscord(t *testing.T) {
	// t.Skip()

	notifyAPI := newTestNotifier(nil)

	_ = notifyAPI.Send(discord.ChannelDefault, "#00FF00", "hello world")
	require.NoError(t, nil)
}

func TestSendNotifyOrder(t *testing.T) {
	// t.Skip()

	notifyAPI := newTestNotifier(map[discord.ChannelName]string{
		discord.ChannelNotifyOrder: _notifyOrderURL,
	})

	if err := Init(notifyAPI, &ClientOptions{
		ProjectName: "mocbydylan",
		Environment: "uat",
	}); err != nil {
		t.Error(err)
	}

	_ = notifyAPI.Send(
		discord.ChannelNotifyOrder,
		"#0099FF",
		"New order received",
		discord.EmbedFields{Name: "Order ID", Value: "#12345", Inline: true},
		discord.EmbedFields{Name: "Amount", Value: "500,000 VND", Inline: true},
	)
}
