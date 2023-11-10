.PHONY: build webapp init

build: webapp

webapp:
	@mkdir -p bin
	go build -o bin/ ./cmd/webapp

webapp-linux:
	@mkdir -p bin/linux_amd64/
	GOOS=linux GOARCH=amd64 go build -o bin/linux_amd64/ ./cmd/webapp

sql:
	./bin/sqlc -f internal/storage/sqlc.yaml generate

# to set things up initially
init:
	GOBIN=`pwd`/bin go install github.com/kyleconroy/sqlc/cmd/sqlc@latest
