FROM almalinux:9

# Install development tools and SSSD
RUN dnf -y update && \
    dnf -y install dnf-plugins-core && \
    dnf config-manager --set-enabled crb && \
    dnf -y install --allowerasing \
    # Build tools
    gcc \
    gcc-c++ \
    make \
    autoconf \
    automake \
    libtool \
    git \
    tar \
    curl \
    gettext \
    pkgconfig \
    # Go
    golang \
    # SSSD runtime
    sssd \
    sssd-client \
    sssd-tools \
    # SSSD build dependencies (for building custom SSSD in tests)
    libtalloc-devel \
    libtdb-devel \
    libtevent-devel \
    libldb-devel \
    popt-devel \
    openldap-devel \
    krb5-devel \
    c-ares-devel \
    pcre2-devel \
    dbus-devel \
    libdhash-devel \
    libcollection-devel \
    libini_config-devel \
    pam-devel \
    jansson-devel \
    libunistring-devel \
    libxslt \
    p11-kit-devel \
    libuuid-devel \
    # Utilities
    procps-ng \
    vim \
    less \
    # Pre-commit and linting tools
    python3-pip \
    && dnf clean all

# Install Node.js 20 from NodeSource
RUN curl -fsSL https://rpm.nodesource.com/setup_20.x | bash - && \
    dnf -y install nodejs && \
    dnf clean all

# Set up Go environment
ENV GOPATH=/go
ENV PATH=$PATH:$GOPATH/bin:/usr/local/go/bin

# Create vscode user and Go directories
RUN useradd -m -s /bin/bash -u 1001 vscode && \
    mkdir -p /home/vscode/.cache /go && \
    chown -R vscode:vscode /home/vscode /go

# Install Go tools for development (as vscode user)
USER vscode
RUN go install golang.org/x/tools/gopls@latest && \
    go install github.com/securego/gosec/v2/cmd/gosec@latest && \
    go install honnef.co/go/tools/cmd/staticcheck@latest && \
    go install gotest.tools/gotestsum@latest && \
    go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Switch back to root for remaining setup
USER root

# Install pre-commit
RUN python3 -m pip install --quiet pre-commit

# Create workspace directory and install pre-commit hooks
WORKDIR /workspace

# Copy pre-commit config and install hook environments
COPY .pre-commit-config.yaml /tmp/
RUN TMPDIR=$(mktemp -d) && \
    cd "$TMPDIR" && \
    git init && \
    cp /tmp/.pre-commit-config.yaml . && \
    pre-commit install-hooks && \
    cd / && \
    rm -rf "$TMPDIR"

# Keep container running
CMD ["/bin/bash"]
