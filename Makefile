GO ?= go
VERSION ?= dev
GO_ENV := GOROOT= GOTOOLCHAIN=local GOPROXY=off GOWORK=off GOFLAGS=

ifeq ($(VERSION),dev)
LDFLAGS :=
else
COMMIT ?= $(shell git rev-parse HEAD)
BUILD_TIME ?= $(shell git show -s --format=%cI HEAD)
GO_VERSION ?= $(shell GOROOT= GOTOOLCHAIN=local GOPROXY=off GOWORK=off GOFLAGS= $(GO) env GOVERSION)
DIRTY ?= $(shell test -z "$$(git status --porcelain)" && printf false || printf true)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME) -X main.goVersion=$(GO_VERSION) -X main.dirty=$(DIRTY)
endif

.PHONY: build test test-race vet coverage integration vmtest vmtest-unit check clean

build:
	mkdir -p bin
	$(GO_ENV) $(GO) build -mod=readonly -trimpath -ldflags "$(LDFLAGS)" -o bin/ldclean ./cmd/ldclean
	$(GO_ENV) $(GO) build -mod=readonly -trimpath -o bin/linux-deep-clean-helper ./cmd/linux-deep-clean-helper

test:
	$(GO_ENV) $(GO) test -mod=readonly ./...

test-race:
	$(GO_ENV) $(GO) test -mod=readonly -race ./...

vet:
	$(GO_ENV) $(GO) vet -mod=readonly ./...

coverage:
	rm -rf coverage
	mkdir -p coverage
	$(GO_ENV) $(GO) test -mod=readonly -covermode=atomic -coverprofile=coverage/application.out ./internal/application
	$(GO_ENV) $(GO) tool cover -func=coverage/application.out | tee coverage/application.func
	grep -Eq 'root_guard\.go:[0-9]+:[[:space:]]+RequireUnprivileged[[:space:]]+100\.0%' coverage/application.func
	grep -Eq 'build_info\.go:[0-9]+:[[:space:]]+Validate[[:space:]]+100\.0%' coverage/application.func
	$(GO_ENV) $(GO) test -mod=readonly -covermode=atomic -coverprofile=coverage/cli.out ./internal/presenters/cli
	$(GO_ENV) $(GO) tool cover -func=coverage/cli.out | tee coverage/cli.func
	grep -Eq 'root\.go:[0-9]+:[[:space:]]+Execute[[:space:]]+100\.0%' coverage/cli.func
	$(GO_ENV) $(GO) test -mod=readonly -covermode=atomic -coverprofile=coverage/helper.out ./cmd/linux-deep-clean-helper
	$(GO_ENV) $(GO) tool cover -func=coverage/helper.out | tee coverage/helper.func
	grep -Eq 'main\.go:[0-9]+:[[:space:]]+run[[:space:]]+100\.0%' coverage/helper.func

integration:
	$(GO_ENV) $(GO) test -mod=readonly -tags=integration ./tests/integration -count=1

vmtest:
	$(GO_ENV) $(GO) test -mod=readonly -tags=vmtest ./tests/vm -count=1

vmtest-unit:
	$(GO_ENV) $(GO) test -mod=readonly -tags=vmtest,vmguardunit ./tests/vm -count=1

check: test test-race vet coverage

clean:
	rm -rf bin coverage coverage-integration coverage.out
