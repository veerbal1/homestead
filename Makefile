
SHA := $(shell git rev-parse --short HEAD)
docker-build:
	docker build --build-arg VERSION=$(SHA) -t homestead:$(SHA) .

docker-run:
	docker run --rm -p 8080:8080 --env-file .env --name homestead homestead:$(SHA)