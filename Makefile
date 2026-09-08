VERSION ?= 0.1.0

.PHONY: build web test package clean

build: web
	cd hub && go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o tmux-hub .

web:
	npm --prefix hub/web ci
	npm --prefix hub/web run build

test: web
	cd hub && go test ./...
	cd hub && go vet ./...

package:
	VERSION=$(VERSION) ./scripts/package.sh

clean:
	rm -rf hub/web/dist hub/tmux-hub release
