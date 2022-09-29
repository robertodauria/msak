package main

import (
	"context"
	"flag"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/m-lab/go/rtx"
	"github.com/robertodauria/msak/client"
	"github.com/robertodauria/msak/client/config"
	"github.com/robertodauria/msak/pkg/ndtm/spec"
	"go.uber.org/zap"
)

var (
	flagServer   = flag.String("server", "localhost:8080", "Server address")
	flagStreams  = flag.Int("streams", 1, "Number of streams")
	flagCC       = flag.String("cc", "bbr", "Congestion control algorithm to use")
	flagDelay    = flag.Duration("delay", 0, "Delay between each stream")
	flagDuration = flag.Duration("duration", 10*time.Second, "Length of the last stream")
	flagScheme   = flag.String("scheme", "ws", "Websocket scheme (wss or ws)")
)

func main() {
	flag.Parse()
	logger, err := zap.NewDevelopment()
	rtx.Must(err, "cannot initialize logger")
	zap.ReplaceGlobals(logger)

	if float64(*flagStreams-1)*flagDelay.Seconds() >= flagDuration.Seconds() {
		zap.L().Sugar().Error("Invalid configuration: please check streams, delay and duration and make sure they make sense.")
		os.Exit(1)
	}
	c := client.NewWithConfig(*flagServer, nil,
		config.New(config.DialerScheme(*flagScheme), *flagDuration, *flagDelay, *flagCC))
	c.StartN(context.Background(), spec.SubtestDownload, *flagStreams, uuid.NewString())
}
