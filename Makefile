# kubernetes-mcp — dev shortcuts
BINARY      := kubernetes-mcp
PKG         := github.com/truepace-io-oss/kubernetes-mcp-server
VERSION     ?= dev
LDFLAGS     := -s -w -X main.version=$(VERSION)
ENVTEST_K8S := 1.34.0

.PHONY: build test test-e2e test-e2e-kind lint helm-lint run tidy fmt

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

## Unit tests (no external API needed)
test:
	go test ./internal/... -race -count=1

## E2E against a real kube-apiserver via envtest (downloads binaries on first run)
test-e2e:
	KUBEBUILDER_ASSETS="$$(setup-envtest use $(ENVTEST_K8S) -p path)" \
		go test ./test/e2e/... -race -count=1 -v

## E2E against a real kind cluster with running workloads
test-e2e-kind:
	go test -tags e2e_kind ./test/e2e/kind/... -count=1 -v

lint:
	go vet ./...
	gofmt -l -d .

fmt:
	gofmt -w .

helm-lint:
	helm lint deploy/helm/kubernetes-mcp

run:
	go run . --config examples/config.yaml

tidy:
	go mod tidy
