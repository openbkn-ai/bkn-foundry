.PHONY: test vet validate ci

test:
	go test ./... -count=1

vet:
	go vet ./...

validate:
	./scripts/validate-datasets.sh

ci: vet test validate
