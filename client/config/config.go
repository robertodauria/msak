package config

import "time"

type DialerProtocol string

const (
	WebSocketSecure DialerProtocol = "wss"
	WebSocket                      = "ws"
)

const (
	DefaultProtocol     = WebSocketSecure
	DefaultTimeout      = 10 * time.Second
	DefaultDuration     = 10 * time.Second
	DefaultStreamsDelay = 0
)

type ClientConfig struct {
	// The protocol to use (ws/wss).
	Protocol DialerProtocol

	// The connection Timeout.
	Timeout time.Duration

	// The default Duration of a measurement.
	Duration time.Duration

	// The delay between stream starts.
	StreamsDelay time.Duration
}

func New(proto DialerProtocol, timeout, duration, delay time.Duration) *ClientConfig {
	return &ClientConfig{
		Protocol:     proto,
		Timeout:      timeout,
		Duration:     duration,
		StreamsDelay: delay,
	}
}

func NewDefault() *ClientConfig {
	return New(DefaultProtocol, DefaultTimeout, DefaultDuration, DefaultStreamsDelay)
}
