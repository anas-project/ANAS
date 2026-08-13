# Networking

## DNS and HTTPS

Traefik is the public HTTP/HTTPS entry point. The selected ACME and DNS capabilities provide certificates. Before deployment, verify:

- base and service domains resolve to the intended entry point;
- DNS API credentials are scoped to modules that need them;
- required TCP and UDP ports are open on the host and upstream router;
- time synchronization is healthy.

## Docker networks and macvlan

Normal services communicate through module-declared Docker networks. Services that must appear directly on the LAN may use macvlan and a narrowly scoped privileged host helper.

A common macvlan property is that the host cannot directly reach containers on the same macvlan. Do not solve this by granting an arbitrary root shell to ANAS; use a constrained helper and sudoers policy.

## Investigation order

1. Verify real DNS answers.
2. Check `anas status` and the active deployment.
3. Check Traefik routers, services, and certificates.
4. Check container health and Docker networks.
5. Check the host firewall, NAT, and upstream routing.
