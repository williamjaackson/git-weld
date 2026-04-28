APP_NAME := git-weld
BIN_DIR := bin

.PHONY: build run clean

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(APP_NAME) ./cmd/git-weld

run:
	go run ./cmd/git-weld

clean:
	rm -rf $(BIN_DIR)
