# Tracker Setup:


This documents the basic setup flow for gount.

1. Download the binary, or build it from source with Go 1.24 or newer.
2. Put the binary on the server where you want the tracker to run.
3. Start and configure the program with `setup.sh`, or edit `config.yaml` manually.
4. If you run behind nginx, Caddy, or another reverse proxy, add the proxy IP or CIDR to `trusted_proxies` so gount will trust `X-Forwarded-For` / `X-Real-IP`.
5. Point DNS at the server, for example `tracker.example.com`.
6. Add the tracking snippet or tracking pixel to your pages, using the correct domain name.
