# ANAS Incus apt configuration

These files are the deterministic compiled apt configuration expected by
`internal/incusprovision.CompiledAPTConfigFiles`.

Host action code should call `WriteAPTConfigFiles("/", recipe, chineseSpeedup)` or copy the
same generated files to `/etc/anas/incus-apt/` with root ownership before
invoking the backend package phase. Each recipe has its own `.apt.conf`,
`sources.list.d/<recipe>/` and `preferences.d/<recipe>/` selection so one apt
run cannot read every packaged distribution fixture. The backend verifies the
installed files byte-for-byte and rejects extra files in the selected source
and preference directories before running `apt-get update` and `apt-get
install`.

The checked-in fixtures are generated with `CompiledAPTConfigFiles(recipe,
false)` and retain the upstream distribution archives. The confirmed request's
`chinese_speedup` boolean selects the alternate compiled policy: Debian main,
updates and security use `https://mirrors.aliyun.com/debian` and
`https://mirrors.aliyun.com/debian-security`; Ubuntu amd64 uses
`https://mirrors.aliyun.com/ubuntu` and arm64 uses
`https://mirrors.aliyun.com/ubuntu-ports`. Source suites, architectures and
archive signing keys are unchanged, and origin pins switch to
`mirrors.aliyun.com`. Compilation, installation and verification use the same
explicit preference, never the process environment or caller-provided URLs.
The installer can replace either exact compiled variant of a recipe, allowing
the preference to be toggled in a fresh confirmed installation plan. It still
rejects unrecognized existing bytes. Package removal accepts either complete
compiled policy without switching sources.

Each `anas.sources` lists the distribution's own archive for dependencies plus
Zabbly's `lts-7.0` Incus repository with its signing key inline in `Signed-By`
(APT checks signatures as `_apt`, which cannot read this root-only tree). Each
`anas.pref` pins `incus`, `incus-base` and `incus-client` to Zabbly and away
from the distribution archive, so a missing Zabbly index cannot fall back to
Incus 6.0. Regenerate these files from `CompiledAPTConfigFiles`; do not edit
them by hand.
