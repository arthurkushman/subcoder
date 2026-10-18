BINARY := subcoder

.PHONY: build test test-race vet fmt lint clean run

build:
	go build -o $(BINARY) .

test:
	go test -p 1 -count=1 ./...

test-race:
	go test -p 1 -race -count=1 ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

lint: fmt vet

clean:
	rm -f $(BINARY)
