# syntax=docker/dockerfile:1
FROM golang:1.18-alpine
RUN apk add gcc git linux-headers musl-dev
WORKDIR /msak

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY ./ ./

RUN ls

RUN ./build.sh

ENTRYPOINT ["./msak-server"]