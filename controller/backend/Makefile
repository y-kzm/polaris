BINARY   := ra-controller
GO       := GOTOOLCHAIN=local go
BUILD    := $(GO) build -o $(BINARY) ./cmd/server/

.PHONY: build run clean tidy

build:
	$(BUILD)

run: build
	./$(BINARY)

tidy:
	$(GO) mod tidy

clean:
	rm -f $(BINARY) controller.db
