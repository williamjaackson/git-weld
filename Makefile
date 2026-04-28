APP_NAME := git-weld
BIN_DIR := bin
INSTALL_DIR ?= /usr/local/bin

.PHONY: build run install clean

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(APP_NAME) ./cmd/git-weld

run:
	go run ./cmd/git-weld

install:
	mkdir -p $(INSTALL_DIR)
	go build -o $(INSTALL_DIR)/$(APP_NAME) ./cmd/git-weld

clean:
	rm -rf $(BIN_DIR)
