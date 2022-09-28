package config

import "time"

type DialerScheme string

const (
	WebSocketSecure DialerScheme = "wss"
	WebSocket                    = "ws"
)

const (
	defaultScheme      = WebSocketSecure
	defaultDuration    = 10 * time.Second
	defaultStreamDelay = 0
	defaultCC          = "bbr"
)

type ClientConfig struct {
	// The scheme to use (ws/wss).
	Scheme DialerScheme

	// The default Duration of a measurement.
	Duration time.Duration

	// The delay between stream starts.
	StreamsDelay time.Duration

	// Ignore invalid TLS certs.
	NoVerify bool

	// Congestion control algorithm to request to the server.
	CongestionControl string
}

func New(scheme DialerScheme, duration, delay time.Duration, cc string) *ClientConfig {
	return &ClientConfig{
		Scheme:            scheme,
		Duration:          duration,
		StreamsDelay:      delay,
		CongestionControl: cc,
	}
}

func NewDefault() *ClientConfig {
	return New(defaultScheme, defaultDuration, defaultStreamDelay, defaultCC)
}
