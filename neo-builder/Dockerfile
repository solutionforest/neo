FROM golang:1.24-alpine3.22

RUN apk add --no-cache git

# Build the neo-builder HTTP service
WORKDIR /app
COPY neo-builder/go.mod neo-builder/main.go ./
RUN go build -o /usr/local/bin/neo-builder .

# Copy Neo source code for compilation
COPY go.mod go.sum* /src/neo/
COPY cmd/ /src/neo/cmd/
COPY commands/ /src/neo/commands/
COPY internal/ /src/neo/internal/

WORKDIR /src/neo
RUN go mod download

VOLUME ["/output"]
EXPOSE 9100

ENTRYPOINT ["neo-builder"]
