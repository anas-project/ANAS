# Operations

This section is for administrators maintaining an ANAS host:

> **Test-target authorization:** use a dedicated non-production environment by
> default. A user or operator may explicitly name the exact server for a test
> run, including a host carrying production services. Explicit selection
> authorizes only the target and never bypasses the isolated Docker daemon,
> workspace, network, port-range, or scoped-cleanup requirements.

- [Storage](storage.md)
- [Networking](networking.md)
- [Troubleshooting](troubleshooting.md)
- [`samba-tool` user and group management](runbooks/samba-tool-user-management.md)

The Chinese documentation also contains service-specific Samba notes and host runbooks; Traefik operations are available in both languages. Test-server addresses, SSH commands, and dated regression reports belong in controlled issues, CI artifacts, or an external private system, not in the public documentation tree.
