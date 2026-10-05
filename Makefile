.PHONY: build test bench vet coverage provenance

build: vet bench

test:
	go test -v -cover -race

provenance:
	go test -v -race -run 'TestProvenance|ExampleSection_Provenance'

bench:
	go test -v -cover -test.bench=. -test.benchmem

vet:
	go vet

coverage:
	go test -coverprofile=c.out && go tool cover -html=c.out && rm c.out
