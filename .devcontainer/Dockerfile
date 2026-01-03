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
    nodejs \
    npm \
    && dnf clean all

# Set up Go environment
ENV GOPATH=/go
ENV PATH=$PATH:$GOPATH/bin:/usr/local/go/bin

# Install Go tools for development
RUN go install golang.org/x/tools/gopls@latest && \
    go install github.com/securego/gosec/v2/cmd/gosec@latest && \
    go install honnef.co/go/tools/cmd/staticcheck@latest

# Create workspace directory
WORKDIR /workspace

# Keep container running
CMD ["/bin/bash"]
