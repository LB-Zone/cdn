# The image `LB-Zone/infra`'s deploy pipeline builds and ships.
#
# Two things here were broken and both failed the build outright:
#
#   * The base was `golang:1.22-bullseye` while `go.mod` asks for 1.25. The
#     official golang images set `GOTOOLCHAIN=local`, so there is no automatic
#     toolchain download to rescue it — `go mod download` stops with
#     `go.mod requires go >= 1.25.0 (running go 1.22.12; GOTOOLCHAIN=local)`.
#   * ImageMagick was whatever version imagemagick.org happened to serve at
#     build time, fetched by a script that scrapes the release index. That makes
#     two builds of the same commit different artifacts, and an upstream release
#     a potential outage.
#
# Debian rather than Alpine on purpose: `imagick.v3` is cgo, and Go gives
# threads it creates the libc default stack — 8 MiB on glibc, 128 KiB on musl,
# which is not enough for `MagickWand` and segfaults under concurrent resizes.
# The all-in-one development image is Alpine and has to set `PT_GNU_STACK`
# explicitly to compensate; see `deploy/allinone/Dockerfile` in the infra
# repository.
FROM golang:1.25-bookworm

ENV DEBIAN_FRONTEND=noninteractive

# Pinned. Bump it deliberately, in a commit that says why.
ARG IMAGEMAGICK_VERSION=7.1.2-29

# Build dependencies, and the delegate libraries ImageMagick links against.
# Without the jpeg/png/webp delegates ImageMagick loads, resizes nothing, and
# returns the original bytes with a 200 — a silent failure the format check
# below is here to catch.
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    gcc \
    curl \
    ca-certificates \
    build-essential \
    pkg-config \
    libpng-dev \
    libjpeg-dev \
    libtiff-dev \
    libwebp-dev \
    && rm -rf /var/lib/apt/lists/*

# ImageMagick 7 from source: Debian ships 6, whose headers live under `wand/`
# rather than `MagickWand/`, and `imagick.v3` includes the latter.
RUN set -eux; \
    work="$(mktemp -d)"; \
    curl -fsSL -o "$work/im.tar.gz" \
      "https://download.imagemagick.org/archive/releases/ImageMagick-${IMAGEMAGICK_VERSION}.tar.gz"; \
    tar xzf "$work/im.tar.gz" -C "$work"; \
    cd "$work/ImageMagick-${IMAGEMAGICK_VERSION}"; \
    ./configure --disable-static --with-quantum-depth=16 \
      --with-jpeg --with-png --with-webp --with-tiff --without-x --disable-docs; \
    make -j"$(nproc)"; \
    make install; \
    ldconfig /usr/local/lib; \
    for format in JPEG PNG WEBP; do \
      magick -list format | grep -qE "^ *${format}\*? " \
        || { echo "no ${format} coder — resizes would silently return the original"; exit 1; }; \
    done; \
    cd /; rm -rf "$work"

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -trimpath -o main ./cmd/main.go

EXPOSE 9090
ENTRYPOINT ["./main"]
