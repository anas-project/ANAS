package incusingresshost

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type forwardingIPSetObservation struct {
	Identity string
	Members  map[string]uint64
}

var forwardingIPSetInitValue = regexp.MustCompile(`^0x[0-9a-f]{1,8}$`)

func parseForwardingIPSet(body []byte, scope ForwardingKernelScope, instances []ForwardingInstanceProof, knownIdentity string, closed, allowExpired bool) (forwardingIPSetObservation, error) {
	empty := forwardingIPSetObservation{}
	bad := func() (forwardingIPSetObservation, error) {
		return empty, fmt.Errorf("owned compatibility set definition or members changed")
	}
	if scope.Validate() != nil || len(body) == 0 || len(body) > 1<<20 {
		return bad()
	}
	name := forwardingIPSetName(scope)
	header := map[string]string{}
	members := map[string]uint64{}
	allowed := map[string]bool{}
	for _, instance := range instances {
		if instance.ValidateFor(scope) != nil {
			return bad()
		}
		for _, route := range scope.Routes {
			allowed[forwardingIPSetMember(instance, route)] = true
		}
	}
	created := false
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if !created {
			if len(fields) < 9 || fields[0] != "create" || fields[1] != name || fields[2] != "hash:ip,port,ip" || len(fields)%2 != 1 {
				return bad()
			}
			for i := 3; i < len(fields); i += 2 {
				key, value := fields[i], fields[i+1]
				if header[key] != "" {
					return bad()
				}
				header[key] = value
			}
			if header["family"] != "inet" || header["maxelem"] != "8192" || header["timeout"] != strconv.Itoa(int(ForwardingPermitTTL.Seconds())) {
				return bad()
			}
			for key, value := range header {
				switch key {
				case "family", "maxelem", "timeout":
				case "hashsize":
					n, err := strconv.ParseUint(value, 10, 32)
					if err != nil || n < 1024 || n > 131072 || n&(n-1) != 0 {
						return bad()
					}
				case "bucketsize":
					if value != "12" && value != "14" {
						return bad()
					}
				case "initval":
					if !forwardingIPSetInitValue.MatchString(value) {
						return bad()
					}
				default:
					return bad()
				}
			}
			if header["initval"] == "" || header["hashsize"] == "" {
				return bad()
			}
			created = true
			continue
		}
		if len(fields) != 5 || fields[0] != "add" || fields[1] != name || fields[3] != "timeout" || members[fields[2]] != 0 || !allowed[fields[2]] || closed {
			return bad()
		}
		ttl, err := strconv.ParseUint(fields[4], 10, 64)
		if err != nil || ttl == 0 || ttl > uint64(ForwardingPermitTTL.Seconds()) {
			return bad()
		}
		members[fields[2]] = ttl
	}
	if !created || !closed && !allowExpired && len(members) != len(allowed) {
		return bad()
	}
	delete(header, "hashsize") // capacity may grow; the kernel's random salt may not change.
	identity := forwardingHash(header)
	if knownIdentity != "" && identity != knownIdentity {
		return bad()
	}
	return forwardingIPSetObservation{Identity: identity, Members: members}, nil
}

func forwardingIPSetRestore(scope ForwardingKernelScope, instances []ForwardingInstanceProof) []byte {
	var out strings.Builder
	for _, instance := range instances {
		for _, route := range scope.Routes {
			out.WriteString("add " + forwardingIPSetName(scope) + " " + forwardingIPSetMember(instance, route) + " timeout " + strconv.Itoa(int(ForwardingPermitTTL.Seconds())) + "\n")
		}
	}
	return []byte(out.String())
}
