#!/usr/bin/env bash
# Build SWE-bench base image using locally downloaded miniconda instead of
# fetching it from repo.anaconda.com (which Docker container gets 30-56 KB/s
# due to Hyper-V virtual network bottleneck).
#
# The base image must be tagged as "sweb.base.py.x86_64:latest" to match
# what swebench expects.

set -euo pipefail

MINICONDA_FILE="/tmp/Miniconda3-py311_23.11.0-2-Linux-x86_64.sh"
BUILD_DIR="/tmp/sweb-build-base"
IMAGE_NAME="sweb.base.py.x86_64:latest"

echo "=== Building SWE-bench base image: $IMAGE_NAME ==="

# Clean build dir
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"

# Copy miniconda installer into build context so COPY works
cp "$MINICONDA_FILE" "$BUILD_DIR/miniconda.sh"

# Custom Dockerfile: same as swebench's _DOCKERFILE_BASE_PY but uses
# COPY instead of wget for miniconda
cat > "$BUILD_DIR/Dockerfile" << 'DOCKERFILE_EOF'
FROM --platform=linux/x86_64 ubuntu:22.04

ARG DEBIAN_FRONTEND=noninteractive
ENV TZ=Etc/UTC

RUN apt update && apt install -y \
wget \
git \
build-essential \
libffi-dev \
libtiff-dev \
python3 \
python3-pip \
python-is-python3 \
jq \
curl \
locales \
locales-all \
tzdata \
&& rm -rf /var/lib/apt/lists/*

# Install conda from local file (pre-downloaded, avoids ~2h network bottleneck)
COPY miniconda.sh /tmp/miniconda.sh
RUN bash /tmp/miniconda.sh -b -p /opt/miniconda3 && rm /tmp/miniconda.sh
# Add conda to PATH
ENV PATH=/opt/miniconda3/bin:$PATH
# Add conda to shell startup scripts like .bashrc (DO NOT REMOVE THIS)
RUN conda init --all
RUN conda config --append channels conda-forge

RUN adduser --disabled-password --gecos 'dog' nonroot
DOCKERFILE_EOF

echo "Dockerfile:"
cat "$BUILD_DIR/Dockerfile"

echo ""
echo "=== Building base image (this step installs apt packages + conda) ==="
docker build --platform linux/x86_64 -t "$IMAGE_NAME" "$BUILD_DIR"

echo ""
echo "=== Verifying base image ==="
docker images | grep sweb.base

echo ""
echo "=== DONE: Base image $IMAGE_NAME built successfully ==="
