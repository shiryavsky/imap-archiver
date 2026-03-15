.PHONY: build run test clean tidy

BINARY := imap-archiver

build: tidy
	go build -o $(BINARY) .

tidy:
	go mod tidy

test:
	go test ./...

clean:
	rm -f $(BINARY)

# Quick dry-run example (override via env vars).
run: build
	./$(BINARY) \
		--host  $${IMAP_HOST} \
		--user  $${IMAP_USER} \
		--pass  $${IMAP_PASSWORD} \
		--folders "INBOX,Sent" \
		--dry-run \
		-v
