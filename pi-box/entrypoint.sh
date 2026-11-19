#!/usr/bin/env bash
# Runs as root with only NET_ADMIN, SETUID and SETGID: installs the egress
# rules, then execs the command as the host uid with every capability dropped.

set -e

uid="${PIBOX_UID:-1000}"
gid="${PIBOX_GID:-1000}"
unset PIBOX_UID PIBOX_GID

case "$uid$gid" in
  *[!0-9]* | "") echo "pi-box: bad PIBOX_UID/PIBOX_GID" >&2; exit 2 ;;
esac
if [ "$uid" = 0 ] || [ "$gid" = 0 ]; then
  echo "pi-box: refusing to run as root or the root group" >&2
  exit 2
fi

# Docker Desktop's resolver lives in 192.168.65.0/24; only port 53 stays open.
while read -r ns; do
  for proto in udp tcp; do
    iptables -A OUTPUT -d "$ns" -p "$proto" --dport 53 -j ACCEPT
  done
done < <(awk '$1 == "nameserver" && $2 !~ /:/ { print $2 }' /etc/resolv.conf)
iptables -A OUTPUT -o lo -j ACCEPT
for net in 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16 \
  100.64.0.0/10 0.0.0.0/8 224.0.0.0/4 240.0.0.0/4; do
  iptables -A OUTPUT -d "$net" -j REJECT --reject-with icmp-port-unreachable
done
if [ -e /proc/net/if_inet6 ]; then
  ip6tables -A OUTPUT -o lo -j ACCEPT
  for net in fc00::/7 fe80::/10 ff00::/8; do
    ip6tables -A OUTPUT -d "$net" -j REJECT
  done
fi

if ! getent passwd "$uid" >/dev/null; then
  echo "pi:x:$uid:$gid::/home/pi:/bin/bash" >>/etc/passwd
fi
if ! getent group "$gid" >/dev/null; then
  echo "pi-host:x:$gid:" >>/etc/group
fi

export HOME=/home/pi USER=pi
exec setpriv --reuid="$uid" --regid="$gid" --clear-groups \
  --inh-caps=-all --bounding-set=-all --no-new-privs -- "$@"
