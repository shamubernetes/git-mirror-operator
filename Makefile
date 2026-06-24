IMG ?= ghcr.io/shamubernetes/git-mirror-operator:dev
SYNC_IMG ?= ghcr.io/shamubernetes/git-mirror-sync:dev
CONTROLLER_GEN ?= controller-gen

.PHONY: test generate manifests docker-build docker-build-sync run-local vet

test:
	go test ./...

vet:
	go vet ./...

generate:
	go generate ./...

manifests:
	@if command -v $(CONTROLLER_GEN) >/dev/null 2>&1; then \
		$(CONTROLLER_GEN) crd paths=./api/... output:crd:artifacts:config=config/crd; \
	else \
		echo "controller-gen not found; using checked-in manifests under config/"; \
	fi

docker-build:
	docker build -t $(IMG) .

docker-build-sync:
	docker build -f Dockerfile.sync -t $(SYNC_IMG) .

run-local:
	SYNC_IMAGE=$(SYNC_IMG) go run ./cmd/manager --webhook-bind-address=:8082 --health-probe-bind-address=:8081
