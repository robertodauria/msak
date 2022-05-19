package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/m-lab/go/rtx"
	"github.com/robertodauria/msak/internal"
)

var flagEndpointCleartext = flag.String("listen", ":8080", "Listen address/port for cleartext connections")

func main() {
	flag.Parse()

	// The ndt7 listener serving up NDT7 tests, likely on standard ports.
	ndt7Mux := http.NewServeMux()
	ndt7Mux.Handle(internal.DownloadPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var cc string
		cch := r.Header.Get("cc")
		if cch == "" {
			// defaults to bbr
			cc = "bbr"
		} else {
			cc = cch
		}
		if conn, err := internal.Upgrade(w, r); err == nil {
			rates := make(chan internal.Rate)
			go func() {
				for rate := range rates {
					fmt.Printf("Download rate: %v\n", rate)
				}
			}()
			err := internal.Sender(r.Context(), conn, rates, cc)
			if err != nil {
				fmt.Println(err)
			}
		}
	}))
	ndt7Mux.Handle(internal.UploadPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if conn, err := internal.Upgrade(w, r); err == nil {
			rates := make(chan internal.Rate)
			go func() {
				for rate := range rates {
					fmt.Printf("Upload rate: %v\n", rate)
				}
			}()
			err := internal.Receiver(r.Context(), rates, conn)
			if err != nil {
				fmt.Println(err)
			}
		}
	}))

	log.Println("About to listen for ndt7 cleartext tests on " + *flagEndpointCleartext)
	rtx.Must(http.ListenAndServe(*flagEndpointCleartext, ndt7Mux), "Could not start ndt7 cleartext server")
}
