GOARM ?= 7
APP_NAME := docport
ENTRY := cmd/docport/main.go
BUILD_DIR := dist

.PHONY: build-linux-amd64 build-linux-arm64 clean

build-linux-arm64:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 \
	GOOS=linux \
	GOARCH=arm64 \
	go build -ldflags '-w -s' -v -o $(BUILD_DIR)/$(APP_NAME) $(ENTRY)

build-linux-amd64:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 \
	GOOS=linux \
	GOARCH=amd64 \
	go build -ldflags '-w -s' -v -o $(BUILD_DIR)/$(APP_NAME) $(ENTRY)

clean:
	rm -rf $(BUILD_DIR)
