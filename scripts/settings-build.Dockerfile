# 設定程式（Tauri）的建置與冒煙測試環境。選 Ubuntu 22.04：產出的二進位只用 glibc 2.34 以內的符號，
# 在 22.04 以上的桌面都能執行；WebKitGTK 在執行期由使用者的系統提供，不隨套件出貨。
FROM ubuntu:22.04
ARG RUST_VERSION=1.99.0
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl build-essential pkg-config python3 file git \
        libwebkit2gtk-4.1-dev \
        xvfb xauth dbus dbus-x11 imagemagick x11-utils xdotool fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/*
ENV RUSTUP_HOME=/opt/rustup CARGO_HOME=/opt/cargo PATH=/opt/cargo/bin:$PATH
# --no-modify-path：不改任何 shell 設定檔。
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --no-modify-path --profile minimal --default-toolchain ${RUST_VERSION} \
    && chmod -R a+rX /opt/rustup /opt/cargo
