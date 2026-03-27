FROM golang:1.24-alpine AS build

ARG VERSION=dev

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/neo \
    ./cmd/neo

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata

COPY --from=build /out/neo /usr/local/bin/neo

WORKDIR /workspace

ENTRYPOINT ["neo"]
