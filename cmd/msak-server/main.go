package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/m-lab/go/rtx"
	internal "github.com/m-lab/msak/internal"
	"github.com/m-lab/ndt-server/ndt7/listener"
	"github.com/m-lab/ndt-server/ndt7/spec"
)

var flagEndpointCleartext = flag.String("listen", ":8080", "Listen address/port for cleartext connections")

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

	// The ndt7 listener serving up NDT7 tests, likely on standard ports.
	ndt7Mux := http.NewServeMux()
	ndt7Mux.Handle(spec.DownloadURLPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if conn, err := internal.Upgrade(w, r); err == nil {
			rates := make(chan internal.Rate)
			go func() {
				for {
					select {
					case <-r.Context().Done():
						return
					case rate, ok := <-rates:
						if ok {
							fmt.Printf("Download rate: %v\n", rate)
						}
					}
				}
			}()
			err := internal.Sender(r.Context(), rates, conn, true)
			if err != nil {
				fmt.Println(err)
			}
		}
	}))
	ndt7Mux.Handle(spec.UploadURLPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if conn, err := internal.Upgrade(w, r); err == nil {
			rates := make(chan internal.Rate)
			go func() {
				for {
					select {
					case <-r.Context().Done():
						return
					case rate, ok := <-rates:
						if ok {
							fmt.Printf("Upload rate: %v\n", rate)
						}
					}
				}
			}()
			err := internal.Receiver(r.Context(), rates, conn)
			if err != nil {
				fmt.Println(err)
			}
		}
	}))

	ndt7ServerCleartext := httpServer(
		*flagEndpointCleartext,
		ndt7Mux,
	)

	log.Println("About to listen for ndt7 cleartext tests on " + *flagEndpointCleartext)
	rtx.Must(listener.ListenAndServeAsync(ndt7ServerCleartext), "Could not start ndt7 cleartext server")
	<-context.Background().Done()

}
