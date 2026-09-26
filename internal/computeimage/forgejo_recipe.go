package computeimage

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ForgejoRunnerRecipe returns the reviewed default distrobuilder recipe for the
// ephemeral one-job runner image. The release pipeline supplies a pinned
// forgejo-runner binary as a release-owned input; consumers never pass scripts,
// aliases, URLs or hooks into this recipe.
func ForgejoRunnerRecipe(target Target) ([]byte, error) {
	return ForgejoRunnerRecipeFromSource(target, "")
}

func ForgejoRunnerRecipeFromSource(target Target, sourceDir string) ([]byte, error) {
	return ForgejoRunnerRecipeWithOptions(target, ForgejoRunnerRecipeOptions{SourceDir: sourceDir})
}

// ForgejoRunnerRecipeOptions selects release-owned build inputs. The source
// selection is frozen into the recipe, never inherited by BuildOnce from the
// builder's environment after the immutable revision has been reserved.
type ForgejoRunnerRecipeOptions struct {
	SourceDir           string
	ChineseBuildSpeedup bool
}

func ForgejoRunnerRecipeWithOptions(target Target, options ForgejoRunnerRecipeOptions) ([]byte, error) {
	if err := target.validate(); err != nil {
		return nil, ErrArtifactInvalid
	}
	sources, err := readForgejoRunnerImageSources(options.SourceDir)
	if err != nil {
		return nil, err
	}
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[target.Architecture]
	if arch == "" {
		return nil, ErrArtifactInvalid
	}
	bootstrapURL := "https://deb.debian.org/debian"
	buildMirrorAction := ""
	if options.ChineseBuildSpeedup {
		bootstrapURL = "https://mirrors.aliyun.com/debian"
		body, err := os.ReadFile(filepath.Join(sources.sourceDir, "configure-build-mirrors"))
		if err != nil || len(body) == 0 || len(body) > 64<<10 {
			return nil, ErrArtifactUnavailable
		}
		buildMirrorAction = "  - trigger: post-unpack\n    action: |-\n" + indentLiteral(string(body))
	}
	vmFiles := ""
	vmPackages := ""
	vmTarget := ""
	if target.Interface == "incus_vm" {
		vmFiles = `  - generator: fstab
  - generator: incus-agent
`
		switch target.Architecture {
		case "amd64":
			vmPackages = `    - action: install
      architectures:
        - x86_64
      packages:
        - efibootmgr
        - grub-efi-amd64
        - grub2-common
        - linux-image-amd64
`
		case "arm64":
			vmPackages = `    - action: install
      architectures:
        - aarch64
      packages:
        - efibootmgr
        - grub-efi-arm64
        - grub2-common
        - linux-image-arm64
`
		}
		vmTarget = "targets:\n  incus:\n    vm:\n      size: 10737418240\n      filesystem: ext4\n"
	}
	body := fmt.Sprintf(`image:
  description: ANAS Forgejo one-job runner
  distribution: debian
  release: trixie
  architecture: %s
  name: anas-forgejo-runner
  serial: deterministic-release-input
mappings:
  architecture_map: debian
source:
  downloader: debootstrap
  url: %s
  suite: trixie
  components:
    - main
packages:
  manager: apt
  update: true
  cleanup: true
  sets:
    - action: install
      packages:
        - apparmor
        - ca-certificates
        - coreutils
        - dbus
        - dbus-user-session
        - fuse-overlayfs
        - git
        - iproute2
        - podman
        - slirp4netns
        - systemd
        - systemd-sysv
        - systemd-resolved
        - uidmap
        - util-linux
%s
files:
  - generator: hostname
    path: /etc/hostname
  - generator: hosts
    path: /etc/hosts
%s  - generator: copy
    source: sources/forgejo-runner
    path: /usr/local/bin/forgejo-runner
    mode: "0755"
    uid: "0"
    gid: "0"
  - generator: dump
    path: /etc/systemd/network/80-anas-dhcp.network
    mode: "0644"
    uid: "0"
    gid: "0"
    content: |-
      [Match]
      Name=eth* en*

      [Network]
      DHCP=yes
      IPv6AcceptRA=yes
  - generator: dump
    path: /usr/lib/tmpfiles.d/anas-resolver.conf
    mode: "0644"
    uid: "0"
    gid: "0"
    content: |-
      L+ /etc/resolv.conf - - - - /run/systemd/resolve/resolv.conf
  - generator: dump
    path: /usr/local/libexec/anas-forgejo-runner-start
    mode: "0755"
    uid: "0"
    gid: "0"
    content: |-
%s
  - generator: dump
    path: /usr/local/libexec/anas-forgejo-runner-input
    mode: "0755"
    uid: "0"
    gid: "0"
    content: |-
%s
  - generator: dump
    path: /usr/local/libexec/anas-forgejo-one-job
    mode: "0755"
    uid: "0"
    gid: "0"
    content: |-
%s
  - generator: dump
    path: /usr/lib/systemd/user/anas-podman.service
    mode: "0644"
    uid: "0"
    gid: "0"
    content: |-
%s
  - generator: dump
    path: /usr/lib/systemd/user/anas-podman.socket
    mode: "0644"
    uid: "0"
    gid: "0"
    content: |-
%s
  - generator: dump
    path: /usr/lib/tmpfiles.d/anas-podman.conf
    mode: "0644"
    uid: "0"
    gid: "0"
    content: |-
%s
  - generator: dump
    path: /usr/share/anas/forgejo-runner/podman.apparmor
    mode: "0644"
    uid: "0"
    gid: "0"
    content: |-
%s
  - generator: dump
    path: /etc/systemd/system/anas-forgejo-podman-policy.service
    mode: "0644"
    uid: "0"
    gid: "0"
    content: |-
%s
  - generator: dump
    path: /etc/systemd/system/apparmor.service.d/anas-runner.conf
    mode: "0644"
    uid: "0"
    gid: "0"
    content: |-
%s
  - generator: dump
    path: /etc/systemd/system/user@1002.service.d/anas-engine.conf
    mode: "0644"
    uid: "0"
    gid: "0"
    content: |-
%s
  - generator: dump
    path: /etc/forgejo-runner/config.yml
    mode: "0644"
    uid: "0"
    gid: "0"
    content: |-
%s
actions:
%s  - trigger: post-files
    action: |-
      #!/bin/sh
      set -eu
      # The archive is private, but the guest OS root must be traversable by
      # systemd service users. A release operator's umask must not become the
      # rootfs mode and prevent every non-root daemon from starting.
      chmod 0755 /
      # Dump generators create missing parents under the private build umask.
      # These two directories contain only public configuration/executables.
      install -d -o root -g root -m 0755 /etc/forgejo-runner /usr/local/libexec
      install -d -o root -g root -m 0755 /usr/lib/systemd/user /etc/systemd/system/user@1002.service.d
      install -d -o root -g root -m 0755 /etc/systemd/system/apparmor.service.d
      install -d -o root -g root -m 0755 /usr/share/anas /usr/share/anas/forgejo-runner
      groupadd --gid 1003 actions-engine
      useradd --uid 1001 --create-home --shell /usr/sbin/nologin runner-agent
      useradd --uid 1002 --gid actions-engine --no-user-group --create-home --shell /usr/sbin/nologin runner-engine
      usermod --append --groups actions-engine runner-agent
      # Rootless DNS helpers and cgroup scopes use the engine user's bus.
      # Enable this offline; a build chroot has no logind connection.
      install -d -o root -g root -m 0755 /var/lib/systemd/linger
      touch /var/lib/systemd/linger/runner-engine
      chmod 0644 /var/lib/systemd/linger/runner-engine
      grep -q '^runner-engine:' /etc/subuid || usermod --add-subuids 100000-165535 runner-engine
      grep -q '^runner-engine:' /etc/subgid || usermod --add-subgids 100000-165535 runner-engine
      install -d -o runner-agent -g runner-agent -m 0700 /home/runner-agent/.cache/act
      systemctl enable systemd-networkd.service
      systemctl enable systemd-resolved.service
      install -d -o runner-engine -g actions-engine -m 0700 /home/runner-engine/.config /home/runner-engine/.config/systemd /home/runner-engine/.config/systemd/user /home/runner-engine/.config/systemd/user/sockets.target.wants
      ln -s /usr/lib/systemd/user/anas-podman.socket /home/runner-engine/.config/systemd/user/sockets.target.wants/anas-podman.socket
      chown -h runner-engine:actions-engine /home/runner-engine/.config/systemd/user/sockets.target.wants/anas-podman.socket
%s`, arch, bootstrapURL, vmPackages, vmFiles, indentLiteral(sources.runnerStart), indentLiteral(sources.runnerInput), indentLiteral(sources.oneJob), indentLiteral(sources.podmanService), indentLiteral(sources.podmanSocket), indentLiteral(sources.podmanTmpfiles), indentLiteral(sources.podmanAppArmor), indentLiteral(sources.podmanPolicyService), indentLiteral(sources.apparmorLoader), indentLiteral(sources.engineUserManager), indentLiteral(sources.runnerConfig), buildMirrorAction, vmTarget)
	return []byte(body), nil
}

type forgejoRunnerImageSources struct {
	sourceDir           string
	runnerStart         string
	runnerInput         string
	oneJob              string
	podmanService       string
	podmanSocket        string
	podmanTmpfiles      string
	podmanAppArmor      string
	podmanPolicyService string
	apparmorLoader      string
	engineUserManager   string
	runnerConfig        string
}

func readForgejoRunnerImageSources(sourceDir string) (forgejoRunnerImageSources, error) {
	if sourceDir == "" {
		var candidates []string
		if _, file, _, ok := runtime.Caller(0); ok {
			candidates = append(candidates, filepath.Join(filepath.Dir(file), "..", "..", "modules", "forgejo", "runner-image"))
		}
		candidates = append(candidates, "modules/forgejo/runner-image", "../../modules/forgejo/runner-image")
		for _, candidate := range candidates {
			if info, err := os.Stat(filepath.Join(candidate, "anas-forgejo-runner-start")); err == nil && info.Mode().IsRegular() {
				sourceDir = candidate
				break
			}
		}
	}
	if sourceDir == "" || !filepath.IsAbs(sourceDir) && filepath.Clean(sourceDir) == "." {
		return forgejoRunnerImageSources{}, ErrArtifactUnavailable
	}
	read := func(name string) (string, error) {
		body, err := os.ReadFile(filepath.Join(sourceDir, name))
		if err != nil || len(body) == 0 || len(body) > 64<<10 {
			return "", ErrArtifactUnavailable
		}
		return string(body), nil
	}
	runnerStart, err := read("anas-forgejo-runner-start")
	if err != nil {
		return forgejoRunnerImageSources{}, err
	}
	runnerInput, err := read("anas-forgejo-runner-input")
	if err != nil {
		return forgejoRunnerImageSources{}, err
	}
	oneJob, err := read("anas-forgejo-one-job")
	if err != nil {
		return forgejoRunnerImageSources{}, err
	}
	podmanService, err := read("anas-podman.service")
	if err != nil {
		return forgejoRunnerImageSources{}, err
	}
	podmanSocket, err := read("anas-podman.socket")
	if err != nil {
		return forgejoRunnerImageSources{}, err
	}
	podmanTmpfiles, err := read("anas-podman.conf")
	if err != nil {
		return forgejoRunnerImageSources{}, err
	}
	podmanAppArmor, err := read("anas-forgejo-podman.apparmor")
	if err != nil {
		return forgejoRunnerImageSources{}, err
	}
	podmanPolicyService, err := read("anas-forgejo-podman-policy.service")
	if err != nil {
		return forgejoRunnerImageSources{}, err
	}
	apparmorLoader, err := read("anas-apparmor-loader.conf")
	if err != nil {
		return forgejoRunnerImageSources{}, err
	}
	engineUserManager, err := read("anas-engine-user.conf")
	if err != nil {
		return forgejoRunnerImageSources{}, err
	}
	runnerConfig, err := read("config.yml")
	if err != nil {
		return forgejoRunnerImageSources{}, err
	}
	return forgejoRunnerImageSources{sourceDir: sourceDir, runnerStart: runnerStart, runnerInput: runnerInput, oneJob: oneJob, podmanService: podmanService, podmanSocket: podmanSocket, podmanTmpfiles: podmanTmpfiles, podmanAppArmor: podmanAppArmor, podmanPolicyService: podmanPolicyService, apparmorLoader: apparmorLoader, engineUserManager: engineUserManager, runnerConfig: runnerConfig}, nil
}

func indentLiteral(s string) string {
	out := ""
	for _, line := range splitRecipeLines(s) {
		out += "      " + line + "\n"
	}
	return out
}

func splitRecipeLines(s string) []string {
	var lines []string
	start := 0
	for i := range s {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
