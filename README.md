# cjdns-proxy-server

This is a simple proxy server that allows you to access your websites through cjdns.

build it with
```bash
go build -o cjdns-proxy-server
```

edit config.sh to set your domain and cjdns ipv6 address.

then run
```bash
source ./config.sh
./cjdns-proxy-server
```