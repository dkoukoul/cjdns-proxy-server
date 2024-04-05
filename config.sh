export PROXY_TO_HOST="" #yourdomain.nohost.me
export PROXY_FROM_HOST="" #your cjdns ipv6 address fc12:3456:789a:bcde:f123:4567:89ab:cdef no square brackets, but do remove leading 0s with in address sections
export PROXY_PORT="80" #"80" make sure to comment out #listen [::]:80 in /etc/nginx/conf.d/yunohost_admin.conf and /etc/nginx/conf.d/domain.conf
