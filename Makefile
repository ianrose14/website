.PHONY: bin/webapp

bin/webapp:
	@mkdir -p bin
	go build -o bin/ ./cmd/webapp

sql:
	./bin/sqlc -f internal/storage/sqlc.yaml generate

# to set things up initially
init:
	GOBIN=`pwd`/bin go install github.com/kyleconroy/sqlc/cmd/sqlc@latest
