SHELL := /bin/bash
GO      := go
GOCMD   := $(GO)
BINARY  := btt-client
PKGS    := $(shell $(GOCMD) list ./...)
LOGFILE := writes.log
NUM_TORRENT_PIECES := 3015

.PHONY: all build run test clean

all: build

build: 
	$(GOCMD) build -o $(BINARY) .

run: build 
	./$(BINARY)

test: 
	$(GOCMD) test -v -race $(PKGS)

clean:
	rm -f $(BINARY)
	$(GOCMD) clean

find-duplicates:
	@echo "Checking for duplicates"
	@awk '{print $$NF}' $(LOGFILE) | sort -nu | uniq -d
	@echo "Finished"

find-missing:
	@echo "Checking for missing"
	@awk '{print $$NF}' writes.log | sort -nu | comm -23 <(seq 0 3015) -
	@echo "Finished"