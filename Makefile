VERSION ?= 0.3.0
GOLANGCI_LINT_VERSION ?= 2.13.2

.PHONY: build web test package clean lint fmt install-lint

build: web
	cd hub && go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o visual-tmux-client ./cmd/visual-tmux-client

web:
	npm --prefix hub/web ci
	npm --prefix hub/web run build

install-lint:
	./scripts/install-golangci-lint.sh

lint: web
	@which golangci-lint > /dev/null 2>&1 || { \
		echo "golangci-lint not found. Install version $(GOLANGCI_LINT_VERSION) via 'make install-lint' or ./scripts/install-golangci-lint.sh" >&2; \
		exit 1; \
	}
	cd hub && golangci-lint config verify --config ../.golangci.yml
	cd hub && golangci-lint run --config ../.golangci.yml ./...

fmt:
	@which golangci-lint > /dev/null 2>&1 || { \
		echo "golangci-lint not found. Install version $(GOLANGCI_LINT_VERSION) via 'make install-lint' or ./scripts/install-golangci-lint.sh" >&2; \
		exit 1; \
	}
	cd hub && golangci-lint fmt --config ../.golangci.yml

test: web
	npm --prefix hub/web test
	cd hub && go test ./...
	cd hub && go vet ./...

package:
	VERSION=$(VERSION) ./scripts/package.sh

clean:
	rm -rf hub/web/dist hub/visual-tmux-client release
