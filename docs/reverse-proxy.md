# Reverse proxy

Pocket Money serves the UI, the API and the Firefly-compatible API from one port. Terminate TLS in front of it and set `BASE_URL` and `SECURE_COOKIES=true`. Voice dictation and the camera need HTTPS.

## Caddy

```
money.example.com {
	reverse_proxy pocket-money:8080
}
```

## Traefik labels

```yaml
labels:
  traefik.enable: "true"
  traefik.http.routers.money.rule: Host(`money.example.com`)
  traefik.http.routers.money.entrypoints: websecure
  traefik.http.routers.money.tls.certresolver: le
  traefik.http.services.money.loadbalancer.server.port: "8080"
```

## nginx

```
server {
  server_name money.example.com;
  location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    client_max_body_size 60m;
  }
}
```

## Forward auth

If you put Authelia / Authentik / Tinyauth in front and use `AUTH_MODE=proxy`, exclude these paths from the auth rule — their clients send their own token:

- `/firefly/api/*` — the Firefly III-compatible API (Raycast, phone app, other apps, scripts)
- `/cal/*` — the payments calendar carries `?token=`
- `/healthz`, `/metrics`
