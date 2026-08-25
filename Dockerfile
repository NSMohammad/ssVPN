# مرحله کامپایل
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY panel.go .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o web_panel panel.go

# مرحله نهایی کانتینر
FROM alpine:latest
RUN apk update && apk add --no-cache \
    openssh \
    iptables \
    bash \
    shadow \
    dumb-init \
    curl

RUN sed -i 's/#PermitRootLogin.*/PermitRootLogin no/' /etc/ssh/sshd_config && \
    sed -i 's/AllowTcpForwarding no/AllowTcpForwarding yes/' /etc/ssh/sshd_config && \
    sed -i 's/#PermitTunnel.*/PermitTunnel yes/' /etc/ssh/sshd_config && \
    sed -i 's/#GatewayPorts no/GatewayPorts yes/' /etc/ssh/sshd_config && \
    mkdir -p /etc/vpn_users /etc/ssh/keys /var/run/sshd

COPY add_user.sh /usr/local/bin/add_user
COPY monitor.sh /usr/local/bin/monitor
COPY show_traffic.sh /usr/local/bin/show_traffic
COPY setup_firewall.sh /usr/local/bin/setup_firewall.sh
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
COPY --from=builder /app/web_panel /usr/local/bin/web_panel

RUN chmod +x /usr/local/bin/*

ENTRYPOINT ["/usr/bin/dumb-init", "--"]
CMD ["/usr/local/bin/entrypoint.sh"]
