FROM node:22.23.1-bookworm-slim

ARG PI_VERSION=1.5.2

RUN apt-get update \
  && apt-get install -y --no-install-recommends \
    bash ca-certificates curl git iptables ripgrep \
  && rm -rf /var/lib/apt/lists/*

RUN npm install -g --ignore-scripts \
    "@earendil-works/pi-coding-agent@${PI_VERSION}" \
  && npm cache clean --force

# The entrypoint runs pi as the host uid, which may not be 1000.
RUN groupmod -n pi node \
  && usermod -l pi -d /home/pi -m node \
  && mkdir -p /home/pi/.pi/agent \
  && chmod 1777 /home/pi /home/pi/.pi /home/pi/.pi/agent

COPY entrypoint.sh /usr/local/sbin/pi-box-entrypoint
ENTRYPOINT ["/usr/local/sbin/pi-box-entrypoint"]
CMD ["pi"]
