FROM rust:1.90-slim-bookworm AS htmlq
RUN cargo install htmlq --version 0.4.0 --locked --root /out

FROM node:22.23.1-bookworm-slim

ARG PI_VERSION=1.5.2

RUN apt-get update \
  && apt-get install -y --no-install-recommends \
    bash ca-certificates curl git iptables ripgrep \
    wget jq fd-find ffmpeg sqlite3 pandoc python3 libatomic1 \
  && ln -s "$(command -v fdfind)" /usr/local/bin/fd \
  && rm -rf /var/lib/apt/lists/*

RUN npm install -g --ignore-scripts \
    "@earendil-works/pi-coding-agent@${PI_VERSION}" \
  && npm cache clean --force

COPY --from=ghcr.io/astral-sh/uv:0.8.22 /uv /usr/local/bin/uv
COPY --from=oven/bun:1.3.0-slim /usr/local/bin/bun /usr/local/bin/bun
COPY --from=htmlq /out/bin/htmlq /usr/local/bin/htmlq

# Playwright's headless shell is the only browser; no headed one exists.
ENV VIRTUAL_ENV=/opt/venv \
    PATH=/opt/venv/bin:$PATH \
    PLAYWRIGHT_BROWSERS_PATH=/opt/pw-browsers \
    UV_LINK_MODE=copy
COPY requirements.txt /tmp/requirements.txt
RUN uv venv "$VIRTUAL_ENV" --python /usr/bin/python3 \
  && uv pip install --no-cache -r /tmp/requirements.txt \
  && playwright install --with-deps --only-shell chromium \
  && rm -rf /var/lib/apt/lists/* /tmp/requirements.txt \
  && chmod -R a+rX /opt/venv /opt/pw-browsers

COPY bin/ /usr/local/bin/
COPY doctor/ /opt/pi-box/doctor/

# The entrypoint runs pi as the host uid, which may not be 1000.
RUN groupmod -n pi node \
  && usermod -l pi -d /home/pi -m node \
  && mkdir -p /home/pi/.pi/agent \
  && chmod 1777 /home/pi /home/pi/.pi /home/pi/.pi/agent

COPY entrypoint.sh /usr/local/sbin/pi-box-entrypoint
ENTRYPOINT ["/usr/local/sbin/pi-box-entrypoint"]
CMD ["pi"]
