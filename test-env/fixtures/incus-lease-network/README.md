# Native lease network acceptance

Real-host acceptance for the compute lease network (`INCUS-R-112`—`R-164`):
egress tiers, default-deny ingress, HTTP publication through Traefik, port
bindings, revocation through the CLI and the console, the lease network view
(its API, and the rendered page where the VM has Chromium), the boot order
after a host restart and the removal of the host rules by the approved
uninstall. It reuses the
[installed approval fixture](../incus-host-action/README.md) and the
[Core projection inputs](../incus-core-projection/README.md); only the
differences are described here.

## Protected test environment

Use only a fresh disposable QEMU VM whose cloud-init ID is
`anas-incus-host-<six lowercase hex digits>`, on user-mode NAT, with no TAP,
bridge or business Docker. The VM must be allowed to restart once: the outer
owner runs QEMU without `-no-reboot` and restarts the guest between the two
phases.

Unlike the Core projection fixture, Docker here is the distribution's real
`docker.service` with `data-root` set to `/var/lib/anas-host-provision-test`,
so it comes back after the restart and the boot ordering under test
(`incus.service` and the port holds against `docker.service`) is the real one.
The VM also needs `docker-buildx`, Compose v2 (`docker-compose-v2` on Ubuntu,
`docker-compose` on Debian, where the CLI is the separate `docker-cli`
package), `nftables`, `iptables`, `squashfs-tools` and
`xz-utils`, and outbound access to the
mainland Docker Hub mirror `m.daocloud.io` and `mirrors.aliyun.com`: the real
Traefik and lego Modules are built from their own Dockerfiles inside the VM.

VM tier: when `/opt/anas-core-inputs/lease-tiers.json` names consumers
(`{"vm_consumers": ["lnlan", "lnhost"]}`), their leases declare `incus_vm`
and their guests are VMs from the same Debian build
(`guest-vm-incus.tar.xz`, `guest-vm.qcow2`, disk SHA-256 `2daf656f…91cb`); the
others stay containers, and the host is approved for `incus_vm`, whose
preflight requires KVM. All of a consumer's leases share one interface. Each
guest needs at least 512 MiB, so a round runs at most three VM guests in a
3 vCPU / 3.5 GiB VM with nested KVM; cover the four consumers over several
rounds (lnlan with lnhost, lninet, lnmods).

Run it on Ubuntu 26.04 (distribution Docker 29) and on Debian 13 (docker.io 26,
which neither drops unpublished container ports itself nor writes IPv6 rules
by default). On Debian also install `chromium` and `fonts-dejavu-core`: the
driver then opens the lease network page in headless Chromium (`INCUS-R-129`).
Ubuntu ships Chromium only as a snap, so that stage is left out there.

## Inputs

`/opt/anas-core-inputs` holds, besides what the Core projection fixture lists:

- `lease-net-agent`, compiled from `test-env/helpers/lease-net-agent`: the one
  probe program used on the host, in the LAN and internet namespaces, in the
  Docker stand-ins and inside the guests;
- `bundle/modules/{incus,traefik,lego}` with each Module's prebuilt Hook at
  `hook/bin/linux-amd64/anas-hook`, and `bundle/contracts`;
- `guest-incus.tar.xz` and `guest-root.tar.xz`: the Debian trixie container
  image from images.linuxcontainers.org (`20260926_05:24`, metadata SHA-256
  `4c3d8c3d…ed0c2`, rootfs SHA-256 `be6ee6e3…ca69`). The test repacks the
  rootfs as squashfs and supplies it through Core's frozen image path. It is a
  bootable guest for networking checks, not a signed ANAS release image;
- `lnet-auth-hook`, compiled from `test-env/helpers/lnet-auth-hook`: the
  Hook of `lnauth`, a synthetic `forward_auth` provider whose gate is the
  agent's `--auth` mode (`INCUS-R-095`);
- `console-page-probe`, compiled from `test-env/helpers/console-page-probe`:
  drives headless Chromium over the DevTools protocol with the console session
  the driver holds, reports the rendered edges, controls and requests, and
  saves a screenshot;
- `server-incus-lease-network-e2e.py`, this gate's driver.

Every file is listed in `core-manifest.json` with its SHA-256.

## Topology

The VM host plays an Incus host that also runs Docker:

| Stand-in | Addresses | Role |
| --- | --- | --- |
| namespace `anaslan` behind `lnlan0` | `198.51.100.2`, `2001:db8:51::2` | a LAN machine; the subnet is configured as `incus.lan_extra_subnets` |
| namespace `anaswan` behind `lnwan0` | `203.0.113.2`, `2001:db8:203::2`, `169.254.77.2` | an internet host, and a link-local destination |
| container `lnmodule` on `192.168.77.0/24` | `192.168.77.10`, host port `18444` | another Module: a published and an unpublished port |
| host listener | every host address, port `18446` | a host process |

Five leases of four synthetic consumers cover the tiers: `lninet.workers` and
`lninet.side` (`internet`, two leases of one consumer), `lnlan.workers`
(`internet_lan`, `module_access`, `intra_lease`, published: HTTP and an
explicit port binding on a slot; one runtime step redeploys it with
`forward_auth` behind `lnauth` and back), `lnhost.workers` (`internet_lan_host`,
published: `auto` TCP and UDP bindings on a slot), `lnmods.workers`
(`modules_only`). Guests are created and HTTP publications requested through
the shared client from the test process.

## Running

```sh
sudo python3 /opt/anas-core-inputs/server-incus-lease-network-e2e.py \
  --vm-id anas-incus-host-abcdef --phase before-reboot
# the outer owner restarts the VM and waits for it
sudo python3 /opt/anas-core-inputs/server-incus-lease-network-e2e.py \
  --vm-id anas-incus-host-abcdef --phase after-reboot
```

The second phase ends by removing every consumer through the CLI, deleting
the projects, bridges and ACLs that revocation keeps, as an operator would,
and running the approved `incus.uninstall`; it then checks that the lease
rules, port binding table, holds and boot unit are gone and nothing else
changed. The driver keeps the owner password in its private directory so the
second phase can log in again.

Each Go stage records every reachability check, expected and observed, in
`/opt/anas-core-projection/reports/*.json` and fails once with all
mismatches. Public driver evidence is under `/opt/anas-host-action-e2e/reports`;
raw command output for failures is private under
`/opt/anas-core-projection/private`. Archive both report directories, never
the private one, the console sessions, the host bundle or workspace secrets.

Local checks, which do not establish native acceptance:

```sh
go test ./test-env/helpers/lease-net-agent
GOOS=linux go vet ./internal/runner ./test-env/helpers/...
python3 -m unittest discover -s test-env/scripts -p 'test_incus_lease_network_e2e.py'
```
