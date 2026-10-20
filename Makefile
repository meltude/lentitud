VERSION := 0.1.0
BIN     := bin
LDFLAGS := -s -w
GOFLAGS := -trimpath

.PHONY: all build test clean install

all: build

build: $(BIN)/server $(BIN)/client

$(BIN)/%: test
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/$*

test:
	go test ./...

clean:
	rm -rf $(BIN)

install: build
	install -d $(DESTDIR)/usr/local/bin
	install -m 755 $(BIN)/server $(DESTDIR)/usr/local/bin/lentitud-server
	install -m 755 $(BIN)/client $(DESTDIR)/usr/local/bin/lentitud-client