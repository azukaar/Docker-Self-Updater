# syntax=docker/dockerfile:1

FROM debian

WORKDIR /app

COPY build/docker-self-updater .

CMD ["./docker-self-updater"]
