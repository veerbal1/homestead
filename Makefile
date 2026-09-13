
SHA := $(shell git rev-parse --short HEAD)
docker-build:
	docker buildx build --platform linux/amd64 --build-arg VERSION=$(SHA) -t ghcr.io/veerbal1/homestead:$(SHA) --push .

docker-run:
	docker run --rm -p 8080:8080 --env-file .env --name homestead homestead:$(SHA)