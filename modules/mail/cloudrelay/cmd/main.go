package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
)

func main() {
	if len(os.Args) == 7 && os.Args[1] == "bootstrap-install-access" {
		port, err := strconv.Atoi(os.Args[6])
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid connection context")
			os.Exit(1)
		}
		if err = cloudrelay.InstallBootstrapAccess(context.Background(), os.Args[2], os.Args[3], os.Args[4], os.Args[5], port); err != nil {
			fmt.Fprintln(os.Stderr, "bootstrap access installation not confirmed")
			os.Exit(1)
		}
		fmt.Println("bootstrap access installed; verify the actual restricted SSH connection")
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "bootstrap-sudo-check" {
		if err := cloudrelay.CheckBootstrapSudo(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "bootstrap sudo authorization not confirmed")
			os.Exit(1)
		}
		fmt.Println("bootstrap sudo authorization checked for current policy")
		return
	}
	if len(os.Args) == 6 && os.Args[1] == "bootstrap-sshd-check" {
		port, err := strconv.Atoi(os.Args[5])
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid connection context")
			os.Exit(1)
		}
		if err = cloudrelay.CheckBootstrapSSHDContext(context.Background(), os.Args[2], os.Args[3], os.Args[4], port); err != nil {
			fmt.Fprintln(os.Stderr, "SSH context check not confirmed")
			os.Exit(1)
		}
		fmt.Println("SSH authentication checked for this connection context only")
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "bootstrap-access-plan" {
		plan, err := cloudrelay.BootstrapAccessPlanFile(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "bootstrap access plan unavailable")
			os.Exit(1)
		}
		if json.NewEncoder(os.Stdout).Encode(plan) != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "publish-bootstrap" {
		if err := cloudrelay.PublishBootstrapBinary(); err != nil {
			fmt.Fprintln(os.Stderr, "bootstrap publication not confirmed")
			os.Exit(1)
		}
		fmt.Println("bootstrap binary published")
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "bootstrap" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := cloudrelay.Bootstrap(ctx, os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "activate-systemd" {
		err := cloudrelay.ActivateSystemdInstallation(context.Background(), os.Args[2])
		state := "active-local-verified"
		if err != nil {
			state = "unknown"
			if errors.Is(err, cloudrelay.ErrPublishConflict) {
				state = "conflict"
			}
		}
		if e := json.NewEncoder(os.Stdout).Encode(struct {
			State string `json:"state"`
		}{state}); e != nil {
			os.Exit(1)
		}
		if err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "inspect-systemd" {
		state, err := cloudrelay.InspectSystemdInstallation(context.Background(), os.Args[2])
		if err != nil {
			state = "unknown"
			if errors.Is(err, cloudrelay.ErrPublishConflict) {
				state = "conflict"
			}
		}
		if e := json.NewEncoder(os.Stdout).Encode(struct {
			State string `json:"state"`
		}{state}); e != nil {
			os.Exit(1)
		}
		if err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "publish-systemd" {
		err := cloudrelay.PublishSystemdFiles(os.Args[2])
		state := "published"
		if err != nil {
			state = "unknown"
			if errors.Is(err, cloudrelay.ErrPublishConflict) {
				state = "conflict"
			}
		}
		if e := json.NewEncoder(os.Stdout).Encode(struct {
			State string `json:"state"`
		}{state}); e != nil {
			os.Exit(1)
		}
		if err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "run-systemd" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := cloudrelay.RunSystemdRuntime(ctx, os.Getenv("CREDENTIALS_DIRECTORY"), os.Getenv("STATE_DIRECTORY"), func(code string) { fmt.Fprintln(os.Stderr, code) }); err != nil {
			fmt.Fprintln(os.Stderr, "cloud relay systemd startup unavailable")
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "prepare-install" {
		checks, err := cloudrelay.PrepareInstallRuntimeFile(os.Args[2])
		state := "prepared"
		if err != nil {
			state = "not-prepared"
		}
		if errors.Is(err, cloudrelay.ErrInstallOutcomeUnknown) {
			state = "unknown"
		}
		if e := json.NewEncoder(os.Stdout).Encode(struct {
			State  string                      `json:"state"`
			Checks cloudrelay.InstallPreflight `json:"checks"`
		}{state, checks}); e != nil {
			os.Exit(1)
		}
		if err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "preflight" {
		result := cloudrelay.PreflightRuntimeFile(os.Args[2])
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "init" {
		if err := cloudrelay.InitializeRuntimeFile(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "cloud relay initialization unavailable")
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: cloudrelay [init|preflight|prepare-install|publish-systemd|inspect-systemd|activate-systemd] PRIVATE_CONFIG_PATH | cloudrelay run-systemd | cloudrelay bootstrap | cloudrelay publish-bootstrap | cloudrelay bootstrap-access-plan PUBLIC_KEY_FILE | cloudrelay bootstrap-sshd-check REMOTE_IP REMOTE_HOST LOCAL_IP LOCAL_PORT | cloudrelay bootstrap-sudo-check | cloudrelay bootstrap-install-access PUBLIC_KEY_FILE REMOTE_IP REMOTE_HOST LOCAL_IP LOCAL_PORT")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := cloudrelay.RunRuntimeFile(ctx, os.Args[1], func(code string) { fmt.Fprintln(os.Stderr, code) }); err != nil {
		fmt.Fprintln(os.Stderr, "cloud relay unavailable")
		os.Exit(1)
	}
}
