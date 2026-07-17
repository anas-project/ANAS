package main

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const hookABI = "anas.cask/v1"

type hookRequest struct {
	ABI     string            `json:"abi"`
	Phase   string            `json:"phase"`
	Module  string            `json:"module"`
	Workdir string            `json:"workdir"`
	Env     map[string]string `json:"env"`
	Secrets map[string]string `json:"secrets"`
}

type hookResponse struct {
	Env             map[string]string `json:"env,omitempty"`
	Secrets         map[string]string `json:"secrets,omitempty"`
	Files           map[string]string `json:"files,omitempty"`
	DisableServices []string          `json:"disable_services,omitempty"`
	DockerCopies    []dockerCopy      `json:"docker_copies,omitempty"`
}

type dockerCopy struct {
	Source      string `json:"source"`
	Container   string `json:"container"`
	Destination string `json:"destination"`
}

type secretStore struct {
	values map[string]string
}

func (s *secretStore) Ensure(key string, gen func() (string, error)) (string, error) {
	if v := s.values[key]; v != "" {
		return v, nil
	}
	v, err := gen()
	if err != nil {
		return "", err
	}
	s.values[key] = v
	return v, nil
}

type hostNetwork struct {
	IP        net.IP
	Prefix    int
	Interface string
	Gateway   string
}

func main() {
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(err)
	}
	var req hookRequest
	if err := json.Unmarshal(b, &req); err != nil {
		fail(err)
	}
	if req.ABI != hookABI {
		fail(fmt.Errorf("unsupported ABI %q", req.ABI))
	}
	resp, err := handle(req)
	if err != nil {
		fail(err)
	}
	if resp.Env == nil {
		resp.Env = map[string]string{}
	}
	if resp.Secrets == nil {
		resp.Secrets = map[string]string{}
	}
	out, err := json.Marshal(resp)
	if err != nil {
		fail(err)
	}
	fmt.Print(string(out))
}
func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
func handle(req hookRequest) (hookResponse, error) {
	env := cloneMap(req.Env)
	secrets := &secretStore{values: cloneMap(req.Secrets)}
	switch req.Phase {
	case "calculate":
		if err := calculate(req.Module, env, req.Workdir, secrets); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), Secrets: changed(req.Secrets, secrets.values)}, nil
	case "render_env":
		files, err := renderEnv(req.Module, env, req.Workdir)
		if err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), Files: files}, nil
	case "services":
		return hookResponse{DisableServices: disabledServices(req.Module, env)}, nil
	case "after_start":
		return hookResponse{DockerCopies: afterStart(req.Module, env)}, nil
	default:
		return hookResponse{}, nil
	}
}
func calculate(module string, env map[string]string, workdir string, secrets *secretStore) error {
	if module != "core" {
		return nil
	}
	return calcCore(env, workdir, secrets)
}
func renderEnv(module string, env map[string]string, workdir string) (map[string]string, error) {
	if module != "core" {
		return map[string]string{}, nil
	}
	return map[string]string{}, nil
}
func disabledServices(module string, env map[string]string) []string {
	if module != "core" {
		return nil
	}
	return nil
}
func afterStart(module string, env map[string]string) []dockerCopy {
	if module != "core" {
		return nil
	}
	return nil
}
func cloneMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
func changed(old, cur map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range cur {
		if old[k] != v {
			out[k] = v
		}
	}
	return out
}
func calcCore(e map[string]string, _ string, _ *secretStore) error {
	e["DOCKER_ALPINE_VERSION"] = "3.15"
	if e["SERVER_NAME"] == "" {
		if h, err := os.Hostname(); err == nil {
			e["SERVER_NAME"] = strings.ToUpper(strings.Split(h, ".")[0])
		}
	} else {
		e["SERVER_NAME"] = strings.ToUpper(e["SERVER_NAME"])
	}
	if e["BASICAUTH_HTPASSWD"] == "" {
		pass := e["BASICAUTH_PASSWD"]
		if pass == "" {
			pass = e["DEFAULT_SERVICE_ROOT_PASSWORD"]
		}
		sum := sha1.Sum([]byte(pass))
		e["BASICAUTH_HTPASSWD"] = e["BASICAUTH_USER"] + ":{SHA}" + base64.StdEncoding.EncodeToString(sum[:])
	}
	if e["HOST_DNS_SERVER"] == "" {
		e["HOST_DNS_SERVER"] = resolvNameservers()
		if e["HOST_DNS_SERVER"] == "" {
			e["HOST_DNS_SERVER"] = e["DNS_SERVER"]
		}
	}
	if e["HOST_IP"] == "" || e["INTERFACE"] == "" || e["HOST_SUBNET_MASK"] == "" {
		host, err := detectHostNetwork(e["HOST_IP"])
		if err != nil {
			return err
		}
		if e["HOST_IP"] == "" {
			e["HOST_IP"] = host.IP.String()
		}
		e["INTERFACE"] = defaultValue(e["INTERFACE"], host.Interface)
		e["DEFAULT_GATEWAY_IP"] = defaultValue(e["DEFAULT_GATEWAY_IP"], host.Gateway)
		e["HOST_SUBNET_MASK"] = defaultValue(e["HOST_SUBNET_MASK"], fmt.Sprintf("%d", host.Prefix))
	}
	if e["INTERFACE"] == "" || e["HOST_IP"] == "" || e["HOST_SUBNET_MASK"] == "" {
		return fmt.Errorf("could not detect host interface, host ip, or subnet mask")
	}
	vlan, err := calcVLAN(e["HOST_IP"], e["HOST_SUBNET_MASK"])
	if err != nil {
		return err
	}
	if e["VLAN_GATEWAY_IP"] == "" && e["DEFAULT_GATEWAY_IP"] != "" {
		e["VLAN_GATEWAY_IP"] = e["DEFAULT_GATEWAY_IP"]
	}
	for k, v := range vlan {
		if e[k] == "" {
			e[k] = v
		}
	}
	e["LOCAL_DNS_SERVER"] = defaultValue(e["LOCAL_DNS_SERVER"], e["HOST_IP"])
	e["USERDATA_PATH"] = defaultValue(e["USERDATA_PATH"], filepath.Join(e["DATA_PATH"], e["USERDATA_NAME"]))
	e["DOWNLOAD_DIR_NAME"] = defaultValue(e["DOWNLOAD_DIR_NAME"], "Downloads")
	e["MUSIC_DIR_NAME"] = defaultValue(e["MUSIC_DIR_NAME"], "Music")
	e["VIDEO_DIR_NAME"] = defaultValue(e["VIDEO_DIR_NAME"], "Video")
	return nil
}
func defaultValue(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
func detectHostNetwork(preferred string) (hostNetwork, error) {
	gateway, ifaceName := defaultRoute()
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if ifaceName != "" && iface.Name != ifaceName {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}
			if preferred != "" && ip.String() != preferred {
				continue
			}
			ones, _ := ipnet.Mask.Size()
			return hostNetwork{IP: ip, Prefix: ones, Interface: iface.Name, Gateway: gateway}, nil
		}
	}
	if preferred != "" {
		for _, iface := range ifaces {
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				ipnet, ok := addr.(*net.IPNet)
				if !ok {
					continue
				}
				ip := ipnet.IP.To4()
				if ip != nil && ip.String() == preferred {
					ones, _ := ipnet.Mask.Size()
					return hostNetwork{IP: ip, Prefix: ones, Interface: iface.Name, Gateway: gateway}, nil
				}
			}
		}
	}
	return hostNetwork{}, fmt.Errorf("no usable IPv4 address detected")
}
func defaultRoute() (gateway, iface string) {
	out, err := exec.Command("ip", "-4", "route", "show", "default").Output()
	if err != nil {
		return "", ""
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "via" && i+1 < len(fields) {
			gateway = fields[i+1]
		}
		if f == "dev" && i+1 < len(fields) {
			iface = fields[i+1]
		}
	}
	return gateway, iface
}
func resolvNameservers() string {
	b, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return ""
	}
	out := []string{}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "nameserver" {
			out = append(out, fields[1])
		}
	}
	return strings.Join(out, " ")
}
func calcVLAN(ipStr, prefixStr string) (map[string]string, error) {
	ip := net.ParseIP(ipStr).To4()
	if ip == nil {
		return nil, fmt.Errorf("invalid HOST_IP %q", ipStr)
	}
	prefix, err := strconv.Atoi(prefixStr)
	if err != nil || prefix <= 0 || prefix > 28 {
		return nil, fmt.Errorf("invalid HOST_SUBNET_MASK %q for macvlan", prefixStr)
	}
	mask := net.CIDRMask(prefix, 32)
	network := ip.Mask(mask)
	hostSegment := fmt.Sprintf("%s/%d", network.String(), prefix)
	vlanMask := 28
	hostBits := 32 - prefix
	broadcastN := ipToUint32(network) ^ uint32((1<<hostBits)-1)
	vlanNetwork := uint32ToIP(broadcastN & ^uint32((1<<(32-vlanMask))-1))
	bridgeIP := uint32ToIP(ipToUint32(vlanNetwork) + 1)
	return map[string]string{
		"HOST_SEGMENT":          hostSegment,
		"HOST_SEGMENT_FULL":     network.String() + "/" + net.IP(mask).String(),
		"VLAN_SUBNET_MASK":      fmt.Sprintf("%d", vlanMask),
		"VLAN_PREFIX":           vlanNetwork.String(),
		"VLAN_BRIDGE_INTERFACE": "anas_bridge",
		"VLAN_BRIDGE_IP":        bridgeIP.String(),
		"VLAN_SEGMENT":          fmt.Sprintf("%s/%d", vlanNetwork.String(), vlanMask),
		"VLAN_GATEWAY_IP":       ip.String(),
		"VLAN_INTERFACE":        "anas_macvlan",
	}, nil
}
func ipToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	return binary.BigEndian.Uint32(ip)
}
func uint32ToIP(v uint32) net.IP {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return net.IPv4(b[0], b[1], b[2], b[3]).To4()
}
