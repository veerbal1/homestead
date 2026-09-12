
SHA := $(shell git rev-parse --short HEAD)
docker-build:
	docker build --build-arg VERSION=$(SHA) -t homestead:$(SHA) .