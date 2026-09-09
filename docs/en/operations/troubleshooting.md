# Troubleshooting

Collect evidence before changing persistent state:

```bash
anas status -w /srv/anas
anas deployments list -w /srv/anas
docker compose version
docker ps --format 'table {{.Names}}\t{{.Status}}'
```

Then inspect command stderr, container health, and container logs. Do not edit `.anas/state`, frozen deployments, or generated secrets by hand.

| Symptom | Check first |
| --- | --- |
| Module bundle not found | `ANAS_MODULE_ROOT`, installation layout, `--module-root` |
| Precondition failure before apply | configuration, lock file, permissions, storage |
| Container start failure | frozen Compose config, images, container logs |
| HTTPS unavailable | DNS, Traefik router, certificate, ports, firewall |
| Login or directory failure | time sync, Samba/IAM health, protocol binding |
| Data failure after upgrade | stop writes; choose deployment rollback or snapshot restore |

Use `anas rollback` for a bad artifact or configuration. Use `anas snapshot restore` when persistent data has already changed.

## Compensation still blocks jobs after cancellation

A `compensation_checked` event with `result=failed` means recovery after cancellation is incomplete.
The job becomes canceled but retains `NeedsCompensationCheck`, blocking subsequent mutations. The event
contains no raw subprocess error. Check daemon logs and pending transactions, fix Docker or filesystem
access, and retry through the existing compensation recovery flow; clearing the marker alone is not recovery.
For activation failures, inspect `error.detail.primary` before the individual `recovery` outcomes.
