BINARY  ?= gsc
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.Version=$(VERSION)
PKG     := ./cmd/gsc

.PHONY: build install uninstall test vet fmt check clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

# Installs to $(go env GOBIN), or $(go env GOPATH)/bin when GOBIN is unset.
install:
	@dest="$$(go env GOBIN)"; [ -n "$$dest" ] || dest="$$(go env GOPATH)/bin"; \
	  if [ -e "$$dest/gsc" ] || [ -L "$$dest/gsc" ]; then \
	    echo "Refusing to overwrite $$dest/gsc; inspect it and choose an empty GOBIN."; exit 1; fi
	go install -ldflags "$(LDFLAGS)" $(PKG)

uninstall:
	@echo "Use the bundle installer --uninstall, or inspect and remove only your source-built binary. See docs/INSTALL.md."
	@exit 1

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

check: vet test
	@test -z "$$(gofmt -l .)" || (echo "gofmt: files need formatting:" && gofmt -l . && exit 1)

clean:
	rm -f $(BINARY)
