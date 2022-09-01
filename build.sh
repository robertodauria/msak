VERSION=$(git describe --tags)
versionflags="-X github.com/m-lab/ndt-server/version.Version=$VERSION"

COMMIT=$(git log -1 --format=%h)
versionflags="${versionflags} -X github.com/m-lab/go/prometheusx.GitShortCommit=${COMMIT}"

go build -v                                                           \
    -tags netgo                                                        \
    -ldflags "$versionflags -extldflags \"-static\""                   \
    ./cmd/msak-server
