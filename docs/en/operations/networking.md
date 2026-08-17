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

### LAN addresses must be excluded from the DHCP pool

Addresses on the macvlan are allocated by Docker's own IPAM. No DHCP request is made and no duplicate-address detection happens. The defaults come from the top of the host segment (bridge `.241`, container `.242`), which relies on the convention that a router's DHCP pool does not reach that far — not on any agreement.

**Deployment prerequisite: exclude both addresses from the router's DHCP pool, or reserve them there.** A collision is silent; both hosts answer, and the symptom is intermittent connection failure.

Show the current address:

```bash
docker inspect anas_samba_fs --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}'
```

Pin an address instead (recommended — a pinned address is one you chose, and it is the one the conflict probe can check):

```bash
anas config set global.host_lan_ip 192.168.1.51
```

The bridge address is `global.host_lan_bridge_ip`. With both pinned no address pool is carved, so host prefixes narrower than /28 can deploy too.

### Address conflict probe

Before creating the macvlan network the runner probes each address it is about to take: a ping provokes ARP resolution, and the answer is read from the neighbour table. Only `REACHABLE` counts as occupied — a `STALE` entry is usually this deployment's own previous container.

The probe runs only when the network does not yet exist; once it does, the thing answering is our own container. That leaves one gap — an address leased away while the container was down — which the router-side exclusion is the real answer to.

A conflict fails the deployment and names the occupant's MAC. Turn the probe off with `anas config set global.host_lan_arp_check false`.

## Investigation order

1. Verify real DNS answers.
2. Check `anas status` and the active deployment.
3. Check Traefik routers, services, and certificates.
4. Check container health and Docker networks.
5. Check the host firewall, NAT, and upstream routing.
