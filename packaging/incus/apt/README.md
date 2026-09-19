# ANAS Incus apt configuration

These files are the deterministic official apt configuration expected by
`internal/incusprovision.OfficialAPTConfigFiles`.

Host action code should call `WriteAPTConfigFiles("/", recipe)` or copy the
same generated files to `/etc/anas/incus-apt/` with root ownership before
invoking the backend package phase. Each recipe has its own `.apt.conf`,
`sources.list.d/<recipe>/` and `preferences.d/<recipe>/` selection so one apt
run cannot read every packaged distribution fixture. The backend verifies the
installed files byte-for-byte and rejects extra files in the selected source
and preference directories before running `apt-get update` and `apt-get
install`.
