package main

import (
	"context"
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/m-lab/go/httpx"
	"github.com/m-lab/go/rtx"
	"github.com/robertodauria/msak/internal/handler"
	"github.com/robertodauria/msak/pkg/ndtm/spec"
	"go.uber.org/zap"
)

var (
	flagCertFile          = flag.String("cert", "", "The file with server certificates in PEM format.")
	flagKeyFile           = flag.String("key", "", "The file with server key in PEM format.")
	flagEndpoint          = flag.String("wss_addr", ":4443", "Listen address/port for TLS connections")
	flagEndpointCleartext = flag.String("ws_addr", ":8080", "Listen address/port for cleartext connections")
	flagDataDir           = flag.String("datadir", "./data", "Directory to store data in")
	flagDebug             = flag.Bool("debug", false, "Enable info/debug output")
)

// httpServer creates a new *http.Server with explicit Read and Write timeouts.
func httpServer(addr string, handler http.Handler) *http.Server {
	tlsconf := &tls.Config{}
	return &http.Server{
		Addr:      addr,
		Handler:   handler,
		TLSConfig: tlsconf,
		// NOTE: set absolute read and write timeouts for server connections.
		// This prevents clients, or middleboxes, from opening a connection and
		// holding it open indefinitely. This applies equally to TLS and non-TLS
		// servers.
		ReadTimeout:  time.Minute,
		WriteTimeout: time.Minute,
	}
}

func main() {
	flag.Parse()

	if *flagDebug {
		logger, err := zap.NewDevelopment()
		rtx.Must(err, "cannot initialize logger")
		zap.ReplaceGlobals(logger)
	}

	// The ndtm listener serving up ndtm tests, likely on standard ports.
	ndtmMux := http.NewServeMux()
	ndtmHandler := handler.New(*flagDataDir)
	ndtmMux.Handle(spec.DownloadPath, http.HandlerFunc(ndtmHandler.Download))
	ndtmMux.Handle(spec.UploadPath, http.HandlerFunc(ndtmHandler.Upload))
	ndtmServerCleartext := httpServer(
		*flagEndpointCleartext,
		ndtmMux)

	zap.L().Sugar().Info("About to listen for ws tests on " + *flagEndpointCleartext)
	rtx.Must(httpx.ListenAndServeAsync(ndtmServerCleartext), "Could not start cleartext server")

	// Only start TLS-based services if certs and keys are provided
	if *flagCertFile != "" && *flagKeyFile != "" {
		ndt7Server := httpServer(
			*flagEndpoint,
			ndtmMux)
		log.Println("About to listen for wss tests on " + *flagEndpoint)
		rtx.Must(httpx.ListenAndServeTLSAsync(ndt7Server, *flagCertFile, *flagKeyFile), "Could not start TLS server")
		defer ndt7Server.Close()
	}

	<-context.Background().Done()
}
