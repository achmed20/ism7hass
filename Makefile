all: compile

compile:
	GOARCH=amd64 CGO_ENABLED=0 GOOS=linux go build -o .build/bin/ism7-amd64 .
	GOARCH=arm64 CGO_ENABLED=0 GOOS=linux go build -o .build/bin/ism7-arm64 .

run:
	go run . run

lint:
	@command -v golangci-lint > /dev/null || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	golangci-lint run ./...

fmt:
	go fmt ./...

test:
	go test -race ./... -cover

clean:
	rm -rf .build

.PHONY: all compile run lint fmt test clean
