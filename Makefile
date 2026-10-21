VERSION := 0.0.1.1
BIN     := bin
LDFLAGS := -s -w
GOFLAGS += -trimpath

.PHONY: all build test clean install

all: build

build: $(BIN)/lentitud-server $(BIN)/lentitud-client

$(BIN)/%: FORCE
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/$*

FORCE:

test:
	go test ./...

clean:
	rm -rf $(BIN)

install: build
	install -d $(DESTDIR)/usr/local/bin
	install -m 755 $(BIN)/lentitud-server $(DESTDIR)/usr/local/bin/lentitud-server
	install -m 755 $(BIN)/lentitud-client $(DESTDIR)/usr/local/bin/lentitud-client
