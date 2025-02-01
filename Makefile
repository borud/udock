.PHONY: build
.PHONY: minimal
.PHONY: test
.PHONY: vet
.PHONY: staticcheck
.PHONY: lint
.PHONY: clean

all: vet lint staticcheck test

test:
	@echo "*** $@"
	@go test ./...

vet:
	@echo "*** $@"
	@go vet ./...

staticcheck:
	@staticcheck ./...

lint:
	@echo "*** $@"
	@revive ./...

clean:
	@rm -rf bin

install-deps:
	@go install github.com/mgechev/revive@latest
