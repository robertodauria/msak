package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/m-lab/msak/protocol"
)

var flagEndpointCleartext = flag.String("listen", ":80", "Listen address/port for cleartext connections")

func main() {
	flag.Parse()
	http.HandleFunc("/ndt/v7/download", func(w http.ResponseWriter, r *http.Request) {
		if conn, err := protocol.Upgrade(w, r); err == nil {
			protocol.HandleDownload(r.Context(), conn)
		}
	})
	http.HandleFunc("/ndt/v7/upload", func(w http.ResponseWriter, r *http.Request) {
		if conn, err := protocol.Upgrade(w, r); err == nil {
			protocol.HandleUpload(r.Context(), conn)
		}
	})
	go func() {
		log.Fatal(http.ListenAndServe(*flagEndpointCleartext, nil))
	}()
	<-context.Background().Done()
}
