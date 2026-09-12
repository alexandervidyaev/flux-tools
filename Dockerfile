FROM golang:1.25-alpine AS builder

WORKDIR /build

RUN apk add --no-cache git make

COPY go.mod go.sum ./

RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X github.com/alexandervidyaev/flux-tools/internal/cli.version=${VERSION}" -o flux-tools ./cmd/flux-tools

FROM alpine:3.23

ARG KUSTOMIZE_VERSION=5.8.0
ARG HELM_VERSION=3.19.0
ARG KUBECONFORM_VERSION=0.7.0

RUN apk add --no-cache \
    ca-certificates \
    git \
    bash \
    curl \
    yq

RUN curl -sSL "https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2Fv${KUSTOMIZE_VERSION}/kustomize_v${KUSTOMIZE_VERSION}_linux_amd64.tar.gz" | \
    tar xz -C /usr/local/bin && \
    chmod +x /usr/local/bin/kustomize && \
    kustomize version

RUN curl -sSL "https://get.helm.sh/helm-v${HELM_VERSION}-linux-amd64.tar.gz" | \
    tar xz -C /tmp && \
    mv /tmp/linux-amd64/helm /usr/local/bin/helm && \
    rm -rf /tmp/linux-amd64 && \
    chmod +x /usr/local/bin/helm && \
    helm version

RUN curl -sSL "https://github.com/yannh/kubeconform/releases/download/v${KUBECONFORM_VERSION}/kubeconform-linux-amd64.tar.gz" | \
    tar xz -C /usr/local/bin && \
    chmod +x /usr/local/bin/kubeconform && \
    kubeconform -v

COPY --from=builder /build/flux-tools /usr/local/bin/flux-tools

WORKDIR /app

ENV FLUX_TOOLS_CACHE_DIR=/home/flux/.flux-tools/cache \
    FLUX_TOOLS_HELM_TIMEOUT=300 \
    PATH=/usr/local/bin:$PATH

ENTRYPOINT ["flux-tools"]
CMD ["--help"]
