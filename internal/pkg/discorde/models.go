package discorde

import (
	"time"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/pkg/discord"
)

type ClientOptions struct {
	Environment   string
	MaxErrorDepth int
	ProjectName   string
}

type Frame struct {
	Function       string  `json:"function"`
	Module         string  `json:"module"`
	Filename       string  `json:"filename"`
	Lineno         int     `json:"lineno"`
	ProgramCounter uintptr `json:"program_counter"`
}

type Stacktrace struct {
	Frames        []Frame `json:"frames"`
	FramesOmitted []uint  `json:"frames_omitted"`
}

type Exception struct {
	Type       string      `json:"type"`
	Value      string      `json:"value"`
	Module     string      `json:"module"`
	Stacktrace *Stacktrace `json:"stacktrace"`
}

type Event struct {
	Environment string
	ProjectName string
	Tags        map[string]string
	Extras      map[string]interface{}
	Message     string
	Platform    string
	Release     string
	ServerName  string
	Timestamp   time.Time
	Arch        string
	NumCPU      int
	GOOS        string
	GoVersion   string
	Exception   *Exception
	// Channel determines which Discord webhook this event is sent to.
	// Defaults to discord.ChannelDefault when not set.
	Channel discord.ChannelName
}
