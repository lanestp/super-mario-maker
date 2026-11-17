# syntax=docker/dockerfile:1

ARG app_dir="/home/go/app"


# * Building the application
FROM golang:1.27-alpine3.24 AS build
ARG app_dir

WORKDIR ${app_dir}

RUN --mount=type=cache,target=/go/pkg/mod/ \
	--mount=type=bind,source=go.sum,target=go.sum \
	--mount=type=bind,source=go.mod,target=go.mod \
	go mod download -x

COPY . .
ARG BUILD_STRING=pretendo.supermariomaker.docker
RUN --mount=type=cache,target=/go/pkg/mod/ \
	CGO_ENABLED=0 go build -ldflags "-X 'main.serverBuildString=${BUILD_STRING}'" -v -o ${app_dir}/build/server && \
	CGO_ENABLED=0 go build -v -o ${app_dir}/build/smm-account ./cmd/smm-account


# * Building the Wii U plugin the join page hands out. The tools only
# * come for amd64, so this runs emulated on other machines
FROM --platform=linux/amd64 ghcr.io/wiiu-env/wiiupluginsystem:20260503 AS wups
FROM --platform=linux/amd64 ghcr.io/wiiu-env/devkitppc:20260504 AS plugin
COPY --from=wups /artifacts $DEVKITPRO
WORKDIR /plugin
COPY console-plugin/ .
RUN make


# * Running the final application
FROM alpine:3.24 AS final
ARG app_dir
WORKDIR ${app_dir}

RUN addgroup go && adduser -D -G go go

RUN mkdir -p ${app_dir}/log && chown go:go ${app_dir}/log

USER go

COPY --from=build ${app_dir}/build/server ${app_dir}/server
# * For making accounts on a server which keeps its own, with
# * docker exec -it <container> ./smm-account create <username>
COPY --from=build ${app_dir}/build/smm-account ${app_dir}/smm-account
COPY --from=plugin /plugin/smm-private-server.wps ${app_dir}/smm-private-server.wps

CMD [ "./server" ]
