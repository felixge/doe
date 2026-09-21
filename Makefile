.PHONY: lint

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$$(cat .golangci-lint-version) run
