# syntax=docker/dockerfile:1.7

ARG CTYUN_REPO=https://github.com/leleji/CtYun.git
ARG CTYUN_REF=master
ARG APP_VERSION=dev

FROM mcr.microsoft.com/dotnet/sdk:8.0-bookworm-slim AS ctyun-builder

ARG CTYUN_REPO
ARG CTYUN_REF

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
RUN git clone "$CTYUN_REPO" CtYun \
    && cd CtYun \
    && git checkout --detach "$CTYUN_REF"

RUN dotnet restore /src/CtYun/CtYun/CtYun.csproj \
    && dotnet publish /src/CtYun/CtYun/CtYun.csproj \
        --configuration Release \
        --no-restore \
        -p:PublishAot=false \
        --output /out

FROM mcr.microsoft.com/dotnet/runtime:8.0-bookworm-slim

ARG CTYUN_REPO
ARG CTYUN_REF
ARG APP_VERSION

LABEL org.opencontainers.image.title="ctyun-auto" \
      org.opencontainers.image.description="CtYun keep-alive with points automation" \
      org.opencontainers.image.source="${CTYUN_REPO}" \
      org.opencontainers.image.version="${APP_VERSION}" \
      io.ctyun.upstream.revision="${CTYUN_REF}"

USER root
WORKDIR /app

ENV TZ=Asia/Shanghai \
    RUNNING_IN_DOCKER=true \
    CTYUN_DATA_DIR=/app/data \
    PYTHONUNBUFFERED=1 \
    APP_PORT=9845 \
    APP_VERSION=${APP_VERSION} \
    CTYUN_REVISION=${CTYUN_REF} \
    OCR_ENDPOINT=https://orc.1999111.xyz/ocr \
    CAPTCHA_DEBUG=true \
    CAPTCHA_DEBUG_LIMIT=100

RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime \
    && echo "$TZ" > /etc/timezone \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        dos2unix \
        procps \
        python3 \
        python3-pip \
    && rm -rf /var/lib/apt/lists/*

COPY app/requirements.txt /app/requirements.txt
RUN pip3 install \
        --break-system-packages \
        --no-cache-dir \
        --requirement /app/requirements.txt

COPY --from=ctyun-builder /out/ /app/
COPY app/login_script.py /app/login_script.py
COPY app/pc_login.py /app/pc_login.py
COPY app/protocols /app/protocols
COPY app/entrypoint.sh /app/entrypoint.sh
COPY app/ctyun_runner.sh /app/ctyun_runner.sh
COPY app/supervisord.conf /app/supervisord.conf
COPY app/web /app/web
COPY VERSION /app/VERSION

RUN dos2unix /app/entrypoint.sh /app/ctyun_runner.sh \
    && chmod +x /app/entrypoint.sh /app/ctyun_runner.sh

VOLUME ["/app/data"]
EXPOSE 9845

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD python3 -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:9845/health', timeout=3)" || exit 1

ENTRYPOINT ["/app/entrypoint.sh"]
