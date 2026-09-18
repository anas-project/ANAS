package main

import (
	"context"
	"fmt"
)

type storagePool struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Status string `json:"status"`
}

// supportedQuotaPool is a conservative provider admission check, not a host
// acceptance result. Native btrfs/zfs volume limits fail the instance operation
// when they cannot be applied. In contrast, dir can silently skip a root-disk
// quota when its backing filesystem has no project-quota support. The remote
// pool API does not prove that filesystem prerequisite, so we cannot admit it.
// Other drivers require their own source audit and real-host acceptance first.
func supportedQuotaPool(p storagePool, name string) bool {
	return p.Name == name && p.Status == "Created" && (p.Driver == "btrfs" || p.Driver == "zfs")
}

func readQuotaPool(ctx context.Context, c *client, l lease) (bool, error) {
	var pool storagePool
	if err := c.do(ctx, "GET", "/1.0/storage-pools/"+l.StoragePool+"?project=default", nil, &pool); err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("read INCUS_STORAGE_POOL: %w", err)
	}
	return supportedQuotaPool(pool, l.StoragePool), nil
}
