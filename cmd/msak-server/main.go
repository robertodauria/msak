package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/m-lab/go/rtx"
	"github.com/robertodauria/msak/internal"
	"github.com/robertodauria/msak/internal/persistence"
	"github.com/robertodauria/msak/ndt7"
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
		if conn, err := ndt7.Upgrade(w, r); err == nil {
			const buffersize = 64 // the emitter will not wait for us
			measurements := make(chan persistence.Measurement, buffersize)
			go func() {
				for m := range measurements {
					rate := float64(m.AppInfo.NumBytes) / float64(m.AppInfo.ElapsedTime) * 8
					fmt.Printf("Download rate: %v\n", rate)
				}
			}()
			err := ndt7.Sender(r.Context(), conn, measurements, cc)
			if err != nil {
				fmt.Println(err)
			}
		}
	}))
	ndt7Mux.Handle(internal.UploadPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if conn, err := ndt7.Upgrade(w, r); err == nil {
			measurements := make(chan persistence.Measurement)
			go func() {
				for m := range measurements {
					rate := m.AppInfo.ElapsedTime / m.AppInfo.NumBytes * 8
					fmt.Printf("Upload rate: %v\n", rate)
				}
			}()
			err := ndt7.Receiver(r.Context(), measurements, conn)
			if err != nil {
				fmt.Println(err)
			}
		}
	}))

	log.Println("About to listen for ndt7 cleartext tests on " + *flagEndpointCleartext)
	rtx.Must(http.ListenAndServe(*flagEndpointCleartext, ndt7Mux), "Could not start ndt7 cleartext server")
}
