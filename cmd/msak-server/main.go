package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	internal "github.com/m-lab/msak/internal"
)

var flagEndpointCleartext = flag.String("listen", ":80", "Listen address/port for cleartext connections")

func main() {
	flag.Parse()
	http.HandleFunc("/ndt/v7/download", func(w http.ResponseWriter, r *http.Request) {
		if conn, err := internal.Upgrade(w, r); err == nil {
			internal.HandleDownload(r.Context(), conn)
		}
	})
	http.HandleFunc("/ndt/v7/upload", func(w http.ResponseWriter, r *http.Request) {
		if conn, err := internal.Upgrade(w, r); err == nil {
			internal.HandleUpload(r.Context(), conn)
		}
	})
	go func() {
		log.Fatal(http.ListenAndServe(*flagEndpointCleartext, nil))
	}()
	<-context.Background().Done()
}
