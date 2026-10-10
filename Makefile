.PHONY: build test vet bench demo eval verify
VERSION ?= dev
REVISION ?= unknown
build:
	go build -trimpath -ldflags="-s -w -X main.version=$(VERSION) -X main.commit=$(REVISION)" -o bin/searchan ./cmd/searchan
test:
	go test -race -cover -timeout 90s ./...
vet:
	go vet ./...
bench:
	go test -run '^$$' -bench . -benchmem ./reader ./research
demo: build
	./bin/searchan demo
eval: build
	./bin/searchan eval -out artifacts/evaluation.json
verify:
	sh scripts/verify.sh
