go build -v                                                           \
    -tags netgo                                                        \
    -ldflags "$versionflags -extldflags \"-static\""                   \
    ./cmd/msak-server

go build -v 							       \
    -tags netgo                                                        \
    -ldflags "$versionflags -extldflags \"-static\""                   \
    ./cmd/msak-client
