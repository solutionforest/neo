VERSION ?= dev
BINARY  := neo
LDFLAGS := -s -w -X main.version=$(VERSION)
IMAGE   ?= vxero/neo
GO_IMAGE ?= golang:1.24-alpine
HOSTOS ?= $(shell uname -s | sed -e 's/Darwin/darwin/' -e 's/Linux/linux/' -e 's/MINGW.*/windows/' -e 's/MSYS.*/windows/' -e 's/CYGWIN.*/windows/')
HOSTARCH ?= $(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/amd64/amd64/' -e 's/arm64/arm64/' -e 's/aarch64/arm64/')

.PHONY: build build-all build-neotest build-sandbox-test install clean test fmt docker-build docker-run image-build sandbox

DOCKER_GO = docker run --rm -v "$(CURDIR):/src" -w /src $(GO_IMAGE)
GO_BIN = /usr/local/go/bin/go

build:
	@mkdir -p bin
	$(DOCKER_GO) sh -c '$(GO_BIN) mod download && CGO_ENABLED=0 GOOS=$(HOSTOS) GOARCH=$(HOSTARCH) $(GO_BIN) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/neo'

build-all:
	@mkdir -p dist
	$(DOCKER_GO) sh -c '$(GO_BIN) mod download && \
		CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 $(GO_BIN) build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64 ./cmd/neo && \
		CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 $(GO_BIN) build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-amd64 ./cmd/neo && \
		CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 $(GO_BIN) build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 ./cmd/neo && \
		CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 $(GO_BIN) build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 ./cmd/neo && \
		CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO_BIN) build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-windows-amd64.exe ./cmd/neo'

clean:
	rm -rf bin dist

test:
	go test ./...

fmt:
	gofmt -w .

docker-build: image-build

image-build:
	docker build -f Dockerfile.runtime --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

install: build
	sudo cp bin/$(BINARY) /usr/local/bin/$(BINARY)
	@echo "  Installed neo to /usr/local/bin/neo"

build-neotest:
	@mkdir -p bin
	$(DOCKER_GO) sh -c '$(GO_BIN) mod download && CGO_ENABLED=0 GOOS=$(HOSTOS) GOARCH=$(HOSTARCH) $(GO_BIN) build -ldflags "$(LDFLAGS)" -o bin/neotest ./cmd/neotest'

build-sandbox-test:
	@mkdir -p bin
	$(DOCKER_GO) sh -c '$(GO_BIN) mod download && CGO_ENABLED=0 GOOS=$(HOSTOS) GOARCH=$(HOSTARCH) $(GO_BIN) build -ldflags "$(LDFLAGS)" -o bin/neo-sandbox-test ./cmd/neosandbox'

sandbox: build build-sandbox-test
	./test/sandbox/run-tests.sh

sandbox-keep: build build-sandbox-test
	./test/sandbox/run-tests.sh --keep

sandbox-down:
	./test/sandbox/run-tests.sh --down

docker-run:
	mkdir -p $$HOME/.neo
	docker run --rm -it \
		-v $$HOME/.ssh:/root/.ssh:ro \
		-v $$HOME/.neo:/root/.neo \
		-v $$PWD:/workspace \
		-w /workspace \
		$(IMAGE):latest
