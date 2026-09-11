.POSIX:

BINARY := zcodecli
GOFLAGS := -trimpath
LDFLAGS := -linkmode=external

.PHONY: all build clean test fmt vet

all: build

build:
	go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BINARY) .

fmt:
	gofmt -w .

test:
	go test -ldflags="-linkmode=external" ./...

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
