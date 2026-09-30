.PHONY: build run test pi linux clean

build:
	go build -o marco .

run: build
	./marco

test:
	go test ./...

# Raspberry Pi (64-bit OS)
pi:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/marco-linux-arm64 .

linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/marco-linux-amd64 .

clean:
	rm -rf marco dist
