.DEFAULT_GOAL := help
.PHONY: all $(PRODUCER) $(CONSUMER)

# Go build flags
GOOS 	?= darwin
GOARCH	?= amd64
GCFLAGS ?= -l=0

.PHONY: help
help: ## Show help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: clean
clean: ## Clean all
	-rm -fR ./bin

PRODUCER=bin/producer
CONSUMER=bin/consumer
NOTIFIER=bin/notifier
CATCHER=bin/catcher

.PHONY: all
all: $(PRODUCER) $(CONSUMER) $(NOTIFIER) $(CATCHER) ## Make all binaries

$(PRODUCER): $(shell find . -name \*.go)
	go build -tags "$(TAGS)" -gcflags "$(GCFLAGS)" -ldflags "$(LDFLAGS)" -o bin/producer cmd/kafka-producer/*.go

$(CONSUMER): $(shell find . -name \*.go)
	go build -tags "$(TAGS)" -gcflags "$(GCFLAGS)" -ldflags "$(LDFLAGS)" -o bin/consumer cmd/kafka-consumer/*.go

$(NOTIFIER): $(shell find . -name \*.go)
	go build -tags "$(TAGS)" -gcflags "$(GCFLAGS)" -ldflags "$(LDFLAGS)" -o bin/notifier cmd/notifier/*.go

$(CATCHER): $(shell find . -name \*.go)
	go build -tags "$(TAGS)" -gcflags "$(GCFLAGS)" -ldflags "$(LDFLAGS)" -o bin/catcher cmd/catcher/*.go
