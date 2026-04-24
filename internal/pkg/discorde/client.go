package discorde

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/pkg/discord"
)

var (
	hostName, _ = os.Hostname()

	// channelColors maps each channel to its default embed colour.
	channelColors = map[discord.ChannelName]string{
		discord.ChannelDefault:             "#FF0000",
		discord.ChannelWarning:             "#FF0000",
		discord.ChannelNoticeConfigAddress: "#FF0000",
		discord.ChannelNotifyOrder:         "#0099FF",
	}

	_messageColor = "#00FF00"
)

type Client struct {
	options   *ClientOptions
	notifyAPI discord.INotifier
}

func NewClient(notifyAPI discord.INotifier, options *ClientOptions) (*Client, error) {
	if notifyAPI == nil {
		return nil, errors.New("notifyAPI cannot be nil")
	}

	if options.ProjectName == "" {
		return nil, errors.New("project name is required")
	}

	if options.Environment == "" {
		return nil, errors.New("environment is required")
	}

	ops := *options
	ops.MaxErrorDepth = 10

	return &Client{
		options:   &ops,
		notifyAPI: notifyAPI,
	}, nil
}

func (c *Client) eventFromException(exception error) *Event {
	err := exception
	if err == nil {
		err = fmt.Errorf("%s called with nil error", callerFunctionName())
	}

	event := &Event{}
	event.Exception = &Exception{
		Value:      err.Error(),
		Type:       reflect.TypeOf(err).String(),
		Stacktrace: ExtractStacktrace(err),
	}

	if event.Exception.Stacktrace == nil {
		event.Exception.Stacktrace = NewStacktrace()
	}

	var title strings.Builder
	if len(event.Exception.Value) > 100 {
		event.Exception.Value = event.Exception.Value[:100]
	}
	title.WriteString(fmt.Sprintf("%s - %s\n", event.Exception.Type, event.Exception.Value))
	if event.Exception.Stacktrace != nil {
		for _, f := range event.Exception.Stacktrace.Frames {
			title.WriteString(fmt.Sprintf("%s:%d %s(0x%x)\n", f.Filename, f.Lineno, f.Function, f.ProgramCounter))
		}
	}

	event.Message = title.String()
	return event
}

func (c *Client) prepareEvent(event *Event, scope *Scope) *Event {
	event.Timestamp = time.Now()
	event.ServerName = hostName
	event.Platform = "go"
	event.Environment = c.options.Environment
	event.ProjectName = c.options.ProjectName
	event.Release = getReleaseVersion()
	event.Arch = runtime.GOARCH
	event.NumCPU = runtime.NumCPU()
	event.GOOS = runtime.GOOS
	event.GoVersion = runtime.Version()

	if scope != nil {
		event = scope.ApplyToEvent(event)
	}

	if event.Channel == "" {
		event.Channel = discord.ChannelDefault
	}

	return event
}

func extractCommonFields(event *Event) []discord.EmbedFields {
	return []discord.EmbedFields{
		{Name: "Project", Value: event.ProjectName},
		{Name: "Runtime", Value: event.GoVersion},
		{Name: "Server", Value: event.ServerName},
		{Name: "Environment", Value: event.Environment},
	}
}

func (c *Client) buildFields(event *Event) []discord.EmbedFields {
	fields := extractCommonFields(event)
	for k, v := range event.Tags {
		fields = append(fields, discord.EmbedFields{Name: k, Value: v})
	}
	return fields
}

func colorForChannel(channel discord.ChannelName) string {
	if color, ok := channelColors[channel]; ok {
		return color
	}
	return "#FF0000"
}

// CaptureException formats the error and sends it to the channel stored in scope.
func (c *Client) CaptureException(exception error, scope *Scope) {
	event := c.eventFromException(exception)
	event = c.prepareEvent(event, scope)

	fields := c.buildFields(event)
	_ = c.notifyAPI.Send(event.Channel, colorForChannel(event.Channel), event.Message, fields...)
}

// CaptureMessage sends a plain message to the channel stored in scope.
// Falls back to ChannelDefault with green colour when no channel is set.
func (c *Client) CaptureMessage(message string, scope *Scope) {
	event := c.prepareEvent(&Event{Message: message}, scope)

	fields := c.buildFields(event)
	_ = c.notifyAPI.Send(event.Channel, _messageColor, event.Message, fields...)
}
