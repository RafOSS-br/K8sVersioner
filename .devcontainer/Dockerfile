FROM mcr.microsoft.com/devcontainers/go:1-1.21-bookworm

# Install Operator SDK
RUN export ARCH=$(case $(uname -m) in x86_64) echo -n amd64 ;; aarch64) echo -n arm64 ;; *) echo -n $(uname -m) ;; esac) && \
    export OS=$(uname | awk '{print tolower($0)}') && \
    export OPERATOR_SDK_DL_URL=https://github.com/operator-framework/operator-sdk/releases/download/v1.37.0 && \
    curl -LO ${OPERATOR_SDK_DL_URL}/operator-sdk_${OS}_${ARCH} && \
    chmod +x operator-sdk_${OS}_${ARCH} && \
    mv operator-sdk_${OS}_${ARCH} /usr/local/bin/operator-sdk

# Install kind
RUN export KIND_VERSION=0.11.1 && \
    curl -Lo kind https://kind.sigs.k8s.io/dl/v${KIND_VERSION}/kind-linux-amd64 && \
    chmod +x kind && \
    mv kind /usr/local/bin