.PHONY: run-sfu run-web fmt vet

run-sfu:
	go run ./apps/sfu

run-web:
	cd ../kamscode-meet-web && npm run dev

fmt:
	go fmt ./...

vet:
	go vet ./...