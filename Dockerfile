# syntax = docker/dockerfile-upstream:1.1.4-experimental

# The base target provides the base for running various tasks against the source
# code.

FROM golang:1.13 AS base
ENV GO111MODULE on
ENV GOPROXY https://proxy.golang.org
ENV CGO_ENABLED 0
WORKDIR /tmp
RUN curl -sfL https://install.goreleaser.com/github.com/golangci/golangci-lint.sh | bash -s -- -b /go/bin v1.23.3
RUN cd $(mktemp -d) \
  && go mod init tmp \
  && go get mvdan.cc/gofumpt/gofumports
WORKDIR /src
COPY ./go.mod ./
COPY ./go.sum ./
RUN go mod download
RUN go mod verify
COPY ./ ./
RUN go list -mod=readonly all >/dev/null
RUN ! go mod tidy -v 2>&1 | grep .


# The test target performs tests on the source code.

FROM base AS unit-tests-runner
ARG PKGS
RUN --mount=type=cache,id=testspace,target=/tmp --mount=type=cache,target=/root/.cache/go-build go test -v -covermode=atomic -coverprofile=coverage.txt -count 1 ${PKGS}

FROM scratch AS unit-tests
COPY --from=unit-tests-runner /src/coverage.txt /coverage.txt

# The unit-tests-race target performs tests with race detector.

FROM base AS unit-tests-race
ENV CGO_ENABLED 1
ARG PKGS
RUN --mount=type=cache,target=/root/.cache/go-build go test -v -count 1 -race ${PKGS}

# The lint target performs linting on the source code.

FROM base AS lint-go
ENV GOGC=50
RUN --mount=type=cache,target=/root/.cache/go-build /go/bin/golangci-lint run
ARG MODULE
RUN FILES="$(gofumports -l -local ${MODULE} .)" && test -z "${FILES}" || (echo -e "Source code is not formatted with 'gofumports -w -local ${MODULE} .':\n${FILES}"; exit 1)

# The fmt target formats the source code.

FROM base AS fmt-build
ARG MODULE
RUN gofumports -w -local ${MODULE} .

FROM scratch AS fmt
COPY --from=fmt-build /src /

# The markdownlint target performs linting on Markdown files.

FROM node:8.16.1-alpine AS lint-markdown
RUN npm install -g markdownlint-cli
RUN npm i sentences-per-line
WORKDIR /src
COPY --from=base /src .
RUN markdownlint --rules /node_modules/sentences-per-line/index.js .

# The container target builds the container image.

FROM base AS binary
RUN --mount=type=cache,target=/root/.cache/go-build GOOS=linux go build -ldflags "-s -w" -o /metal-metadata-server
RUN chmod +x /metal-metadata-server

FROM scratch AS container
COPY --from=docker.io/autonomy/ca-certificates:v0.1.0 / /
COPY --from=docker.io/autonomy/fhs:v0.1.0 / /
COPY --from=binary /metal-metadata-server /metal-metadata-server
ENTRYPOINT [ "/metal-metadata-server" ]

FROM k8s.gcr.io/hyperkube:v1.17.0 AS release-build
RUN apt update -y \
  && apt install -y curl \
  && curl -LO https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2Fv3.4.0/kustomize_v3.4.0_linux_amd64.tar.gz \
  && tar -xf kustomize_v3.4.0_linux_amd64.tar.gz -C /usr/local/bin \
  && rm kustomize_v3.4.0_linux_amd64.tar.gz
COPY ./config ./config
ARG REGISTRY_AND_USERNAME
ARG NAME
ARG TAG
RUN cd config/server \
  && kustomize edit set image server=${REGISTRY_AND_USERNAME}/${NAME}:${TAG} \
  && cd - \
  && kubectl kustomize config/default >/mms-components.yaml
FROM scratch AS release
COPY --from=release-build /mms-components.yaml /mms-components.yaml
